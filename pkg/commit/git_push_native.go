package commit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type nativePushTarget struct {
	branch      string
	localRef    string
	remote      string
	remoteRef   string
	force       bool
	setUpstream bool
}

// IndeterminateRemoteOutcomeError reports a failed remote command that was
// dispatched. The transport may have completed some or all of its updates
// before local Git reported the error, so callers must not describe it as a
// definite failure or blindly retry it.
type IndeterminateRemoteOutcomeError struct {
	Operation string
	Cause     error
}

func (e *IndeterminateRemoteOutcomeError) Error() string {
	return fmt.Sprintf(
		"%s did not complete cleanly after dispatch; the remote may have updated: %v",
		e.Operation,
		e.Cause,
	)
}

func (e *IndeterminateRemoteOutcomeError) Unwrap() error {
	return e.Cause
}

type TagCreationOutcome string

const (
	TagCreationCreated        TagCreationOutcome = "created"
	TagCreationIndeterminate  TagCreationOutcome = "indeterminate"
	nativeTagReconcileTimeout                    = 30 * time.Second
)

// TagCreationOutcomeError reports that git tag failed after it was dispatched,
// so callers must not blindly retry. Created means the previously absent ref
// was present during reconciliation and must conservatively be treated as
// created by this attempt. Indeterminate means its creation could not be proved.
type TagCreationOutcomeError struct {
	TagRef   string
	ObjectID string
	Outcome  TagCreationOutcome
	Cause    error
}

func (e *TagCreationOutcomeError) Error() string {
	if e.Outcome == TagCreationCreated {
		return fmt.Sprintf(
			"tag creation did not complete cleanly after dispatch; %s now exists at %s and must be treated as created; do not retry: %v",
			e.TagRef,
			e.ObjectID,
			e.Cause,
		)
	}
	return fmt.Sprintf(
		"tag creation did not complete cleanly after dispatch; the outcome for %s is indeterminate; inspect the ref before retrying: %v",
		e.TagRef,
		e.Cause,
	)
}

func (e *TagCreationOutcomeError) Unwrap() error {
	return e.Cause
}

func (g *gitOperations) pushNative(ctx context.Context) (string, error) {
	target, err := g.resolveNativePushTarget(ctx)
	if err != nil {
		return "", err
	}

	refspec := target.localRef + ":" + target.remoteRef
	if target.force {
		refspec = "+" + refspec
	}
	pushArgs := []string{"push", "--no-follow-tags"}
	if target.setUpstream {
		pushArgs = append(pushArgs, "--set-upstream")
	}
	pushArgs = append(pushArgs, "--", target.remote, refspec)
	if _, err := g.runGit(
		ctx,
		"",
		nil,
		pushArgs...,
	); err != nil {
		if dispatchErr := nativeRemoteDispatchError(err, "branch push"); dispatchErr != nil {
			return "", dispatchErr
		}
		return "", fmt.Errorf(
			"failed to push %s to %s as %s: %w",
			target.localRef,
			target.remote,
			target.remoteRef,
			err,
		)
	}

	return g.nativeMergeRequestURL(ctx, target), nil
}

func (g *gitOperations) getLatestTagNative(ctx context.Context) (string, error) {
	output, err := g.runGit(
		ctx,
		"",
		nil,
		"for-each-ref",
		"--format=%(refname:strip=2)",
		"refs/tags/",
	)
	if err != nil {
		return "", fmt.Errorf("failed to list tags: %w", err)
	}

	var tags []nativeSemverTag
	for _, name := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if tag, ok := parseNativeSemverTag(name); ok {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		return "", nil
	}

	sort.Slice(tags, func(i, j int) bool {
		return compareNativeSemverTag(tags[i], tags[j]) > 0
	})
	return tags[0].name, nil
}

func (g *gitOperations) createTagNative(
	ctx context.Context,
	tagName string,
	message string,
) error {
	tagRef, err := g.validateNativeTagRef(ctx, tagName)
	if err != nil {
		return err
	}
	beforeOID, existed, err := g.nativeExactRefOID(ctx, tagRef)
	if err != nil {
		return fmt.Errorf("failed to inspect tag %s before creation: %w", tagRef, err)
	}
	if existed {
		return fmt.Errorf("tag %s already exists at %s", tagRef, beforeOID)
	}

	messagePath, cleanup, err := writeNativeTagMessage(message)
	if err != nil {
		return fmt.Errorf("failed to prepare tag message: %w", err)
	}
	defer cleanup()

	if _, err := g.runGit(
		ctx,
		"",
		nil,
		"tag",
		"-a",
		"-F",
		messagePath,
		"--",
		tagName,
	); err != nil {
		var commandErr *gitCommandError
		if errors.As(err, &commandErr) && commandErr.dispatched {
			return g.reconcileNativeTagCreation(ctx, tagRef, err)
		}
		return fmt.Errorf("failed to create tag %s: %w", tagRef, err)
	}
	return nil
}

func (g *gitOperations) reconcileNativeTagCreation(
	ctx context.Context,
	tagRef string,
	commandErr error,
) error {
	reconcileContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		nativeTagReconcileTimeout,
	)
	defer cancel()

	objectID, exists, inspectErr := g.nativeExactRefOID(reconcileContext, tagRef)
	cause := errors.Join(commandErr, inspectErr)
	if inspectErr == nil && exists {
		return &TagCreationOutcomeError{
			TagRef:   tagRef,
			ObjectID: objectID,
			Outcome:  TagCreationCreated,
			Cause:    cause,
		}
	}
	return &TagCreationOutcomeError{
		TagRef:  tagRef,
		Outcome: TagCreationIndeterminate,
		Cause:   cause,
	}
}

func (g *gitOperations) nativeExactRefOID(
	ctx context.Context,
	ref string,
) (string, bool, error) {
	output, err := g.runGit(ctx, "", nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		if nativeGitCommandExitedWith(err, 1) {
			return "", false, nil
		}
		return "", false, err
	}
	objectID := strings.TrimSpace(string(output))
	if objectID == "" || strings.ContainsAny(objectID, "\r\n") {
		return "", false, fmt.Errorf("git returned an invalid object ID for %s", ref)
	}
	return objectID, true, nil
}

func (g *gitOperations) pushTagNative(ctx context.Context, tagName string) error {
	tagRef, err := g.validateNativeTagRef(ctx, tagName)
	if err != nil {
		return err
	}
	if _, err := g.runGit(ctx, "", nil, "show-ref", "--verify", "--quiet", tagRef); err != nil {
		if nativeGitCommandExitedWith(err, 1) {
			return fmt.Errorf("tag %q does not exist", tagName)
		}
		return fmt.Errorf("failed to resolve tag %q: %w", tagName, err)
	}

	branch, _, err := g.nativeAttachedBranch(ctx)
	if err != nil {
		return err
	}
	remote, err := g.resolveNativePushRemote(ctx, branch)
	if err != nil {
		return err
	}
	if err := g.requireNonMirrorPushRemote(ctx, remote); err != nil {
		return err
	}

	if _, err := g.runGit(
		ctx,
		"",
		nil,
		"push",
		"--no-follow-tags",
		"--",
		remote,
		tagRef+":"+tagRef,
	); err != nil {
		if dispatchErr := nativeRemoteDispatchError(err, "tag push"); dispatchErr != nil {
			return dispatchErr
		}
		return fmt.Errorf("failed to push tag %s to %s: %w", tagRef, remote, err)
	}
	return nil
}

func (g *gitOperations) resolveNativePushTarget(ctx context.Context) (nativePushTarget, error) {
	branch, localRef, err := g.nativeAttachedBranch(ctx)
	if err != nil {
		return nativePushTarget{}, err
	}
	remote, err := g.resolveNativePushRemote(ctx, branch)
	if err != nil {
		return nativePushTarget{}, err
	}
	if err := g.requireNonMirrorPushRemote(ctx, remote); err != nil {
		return nativePushTarget{}, err
	}

	target := nativePushTarget{
		branch:   branch,
		localRef: localRef,
		remote:   remote,
	}
	refspecs, err := g.nativeConfigValues(ctx, "remote."+remote+".push")
	if err != nil {
		return nativePushTarget{}, fmt.Errorf(
			"failed to read push refspecs for remote %q: %w",
			remote,
			err,
		)
	}
	if len(refspecs) != 0 {
		return g.resolveExplicitNativePushRefspec(ctx, target, refspecs)
	}

	mode, configured, err := g.nativeConfigValue(ctx, "push.default")
	if err != nil {
		return nativePushTarget{}, fmt.Errorf("failed to read push.default: %w", err)
	}
	if !configured {
		mode = "simple"
	}
	autoSetupRemote, _, err := g.nativeConfigBool(ctx, "push.autoSetupRemote")
	if err != nil {
		return nativePushTarget{}, fmt.Errorf("failed to read push.autoSetupRemote: %w", err)
	}

	switch mode {
	case "nothing":
		return nativePushTarget{}, errors.New("push.default=nothing refuses an implicit branch push")
	case "matching":
		return nativePushTarget{}, errors.New(
			"push.default=matching may update multiple branches; configure a one-branch destination",
		)
	case "current":
		target.remoteRef = localRef
		target.setUpstream, err = g.shouldAutoSetupUpstream(ctx, branch, autoSetupRemote)
		if err != nil {
			return nativePushTarget{}, err
		}
	case "upstream", "tracking":
		upstreamRemote, upstreamRef, hasUpstream, err := g.nativeUpstreamOptional(ctx, branch)
		if err != nil {
			return nativePushTarget{}, err
		}
		if !hasUpstream {
			if !autoSetupRemote {
				return nativePushTarget{}, fmt.Errorf("branch %q has no upstream branch", branch)
			}
			target.remoteRef = localRef
			target.setUpstream = true
			break
		}
		if upstreamRemote != remote {
			return nativePushTarget{}, fmt.Errorf(
				"push.default=%s requires the upstream remote %q, but the push remote is %q",
				mode,
				upstreamRemote,
				remote,
			)
		}
		target.remoteRef = upstreamRef
	case "simple":
		pullRemote, err := g.resolveNativePullRemote(ctx, branch)
		if err != nil {
			return nativePushTarget{}, err
		}
		if pullRemote != remote {
			// In a triangular workflow Git's simple mode behaves like current.
			target.remoteRef = localRef
			target.setUpstream, err = g.shouldAutoSetupUpstream(ctx, branch, autoSetupRemote)
			if err != nil {
				return nativePushTarget{}, err
			}
			break
		}

		upstreamRemote, upstreamRef, hasUpstream, err := g.nativeUpstreamOptional(ctx, branch)
		if err != nil {
			return nativePushTarget{}, err
		}
		if !hasUpstream {
			if !autoSetupRemote {
				return nativePushTarget{}, fmt.Errorf("branch %q has no upstream branch", branch)
			}
			target.remoteRef = localRef
			target.setUpstream = true
			break
		}
		if upstreamRemote != remote {
			return nativePushTarget{}, fmt.Errorf(
				"upstream remote %q does not match push remote %q",
				upstreamRemote,
				remote,
			)
		}
		if upstreamRef != localRef {
			return nativePushTarget{}, fmt.Errorf(
				"push.default=simple refuses branch %q because its upstream branch is %q",
				branch,
				strings.TrimPrefix(upstreamRef, "refs/heads/"),
			)
		}
		target.remoteRef = localRef
	default:
		return nativePushTarget{}, fmt.Errorf("unsupported push.default value %q", mode)
	}

	if err := g.validateNativeHeadRef(ctx, target.remoteRef); err != nil {
		return nativePushTarget{}, fmt.Errorf("invalid push destination: %w", err)
	}
	return target, nil
}

func (g *gitOperations) resolveExplicitNativePushRefspec(
	ctx context.Context,
	target nativePushTarget,
	refspecs []string,
) (nativePushTarget, error) {
	if len(refspecs) != 1 {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q configures %d push refspecs; refusing a potentially multi-ref push",
			target.remote,
			len(refspecs),
		)
	}

	refspec := refspecs[0]
	if strings.HasPrefix(refspec, "+") {
		target.force = true
		refspec = strings.TrimPrefix(refspec, "+")
	}
	if refspec == ":" || strings.Contains(refspec, "*") || strings.HasPrefix(refspec, "^") {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q push refspec %q is matching, wildcard, or negative; refusing an ambiguous push",
			target.remote,
			refspecs[0],
		)
	}
	if strings.Count(refspec, ":") > 1 {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q has invalid push refspec %q",
			target.remote,
			refspecs[0],
		)
	}

	source, destination, hasDestination := strings.Cut(refspec, ":")
	if source == "" {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q push refspec %q deletes a ref; refusing it for a branch push",
			target.remote,
			refspecs[0],
		)
	}
	if source != "HEAD" && source != target.branch && source != target.localRef {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q push refspec %q does not select the current branch %q",
			target.remote,
			refspecs[0],
			target.branch,
		)
	}
	if source == target.branch {
		if strings.HasPrefix(source, "-") {
			return nativePushTarget{}, fmt.Errorf(
				"remote %q push refspec source %q starts with a dash; use the fully qualified source %q",
				target.remote,
				source,
				target.localRef,
			)
		}
		resolved, err := g.runGit(
			ctx,
			"",
			nil,
			"rev-parse",
			"--symbolic-full-name",
			"--verify",
			source,
		)
		if err != nil {
			return nativePushTarget{}, fmt.Errorf(
				"remote %q push refspec source %q does not resolve uniquely to current branch %q: %w",
				target.remote,
				source,
				target.branch,
				err,
			)
		}
		if strings.TrimSpace(string(resolved)) != target.localRef {
			return nativePushTarget{}, fmt.Errorf(
				"remote %q push refspec source %q does not resolve uniquely to current branch %q",
				target.remote,
				source,
				target.branch,
			)
		}
	}

	if !hasDestination {
		destination = target.localRef
	} else if destination == "" {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q has invalid push refspec %q with an empty destination",
			target.remote,
			refspecs[0],
		)
	} else if !strings.HasPrefix(destination, "refs/") {
		if destination == "HEAD" {
			return nativePushTarget{}, fmt.Errorf(
				"remote %q push refspec %q has an ambiguous HEAD destination",
				target.remote,
				refspecs[0],
			)
		}
		destination = "refs/heads/" + destination
	}
	if err := g.validateNativeHeadRef(ctx, destination); err != nil {
		return nativePushTarget{}, fmt.Errorf(
			"remote %q push refspec %q has an invalid branch destination: %w",
			target.remote,
			refspecs[0],
			err,
		)
	}

	target.remoteRef = destination
	return target, nil
}

func (g *gitOperations) nativeAttachedBranch(ctx context.Context) (string, string, error) {
	output, err := g.runGit(ctx, "", nil, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		if nativeGitCommandExitedWith(err, 1) {
			return "", "", errors.New("cannot push from detached HEAD")
		}
		return "", "", fmt.Errorf("failed to resolve current branch: %w", err)
	}

	localRef := strings.TrimSuffix(string(output), "\n")
	if err := g.validateNativeHeadRef(ctx, localRef); err != nil {
		return "", "", fmt.Errorf("current branch has an invalid ref: %w", err)
	}
	return strings.TrimPrefix(localRef, "refs/heads/"), localRef, nil
}

func (g *gitOperations) resolveNativePushRemote(ctx context.Context, branch string) (string, error) {
	keys := []string{
		"branch." + branch + ".pushRemote",
		"remote.pushDefault",
		"branch." + branch + ".remote",
	}
	for _, key := range keys {
		value, configured, err := g.nativeConfigValue(ctx, key)
		if err != nil {
			return "", fmt.Errorf("failed to read %s: %w", key, err)
		}
		if configured {
			if value == "" {
				return "", fmt.Errorf("%s configures an empty push remote", key)
			}
			return value, nil
		}
	}

	return g.nativeFallbackRemote(ctx)
}

func (g *gitOperations) resolveNativePullRemote(ctx context.Context, branch string) (string, error) {
	remote, configured, err := g.nativeConfigValue(ctx, "branch."+branch+".remote")
	if err != nil {
		return "", fmt.Errorf("failed to read pull remote for branch %q: %w", branch, err)
	}
	if configured {
		if remote == "" {
			return "", fmt.Errorf("branch %q configures an empty remote", branch)
		}
		return remote, nil
	}
	// Git's fetch-side default remains "origin" even when another single
	// remote happens to exist. This distinction is what makes an explicit
	// pushRemote a triangular workflow under push.default=simple.
	return "origin", nil
}

func (g *gitOperations) nativeFallbackRemote(ctx context.Context) (string, error) {
	output, err := g.runGit(ctx, "", nil, "remote")
	if err != nil {
		return "", fmt.Errorf("failed to list Git remotes: %w", err)
	}

	var remotes []string
	for _, remote := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if remote != "" {
			remotes = append(remotes, remote)
		}
	}
	if len(remotes) == 1 {
		return remotes[0], nil
	}
	for _, remote := range remotes {
		if remote == "origin" {
			return remote, nil
		}
	}
	if len(remotes) == 0 {
		return "", errors.New("no push remote is configured")
	}
	return "", errors.New("multiple remotes are configured and no push remote can be selected")
}

func (g *gitOperations) nativeUpstreamOptional(
	ctx context.Context,
	branch string,
) (string, string, bool, error) {
	mergeRefs, err := g.nativeConfigValues(ctx, "branch."+branch+".merge")
	if err != nil {
		return "", "", false, fmt.Errorf("failed to read upstream for branch %q: %w", branch, err)
	}
	if len(mergeRefs) == 0 {
		return "", "", false, nil
	}
	if len(mergeRefs) != 1 {
		return "", "", false, fmt.Errorf(
			"branch %q configures %d upstream merge refs; refusing an ambiguous push",
			branch,
			len(mergeRefs),
		)
	}
	if err := g.validateNativeHeadRef(ctx, mergeRefs[0]); err != nil {
		return "", "", false, fmt.Errorf("branch %q has an invalid upstream ref: %w", branch, err)
	}

	remote, err := g.resolveNativePullRemote(ctx, branch)
	if err != nil {
		return "", "", false, err
	}
	return remote, mergeRefs[0], true, nil
}

func (g *gitOperations) shouldAutoSetupUpstream(
	ctx context.Context,
	branch string,
	autoSetupRemote bool,
) (bool, error) {
	if !autoSetupRemote {
		return false, nil
	}
	mergeRefs, err := g.nativeConfigValues(ctx, "branch."+branch+".merge")
	if err != nil {
		return false, fmt.Errorf("failed to inspect upstream for branch %q: %w", branch, err)
	}
	return len(mergeRefs) == 0, nil
}

func (g *gitOperations) nativeConfigValue(
	ctx context.Context,
	key string,
) (string, bool, error) {
	values, err := g.nativeConfigValues(ctx, key)
	if err != nil || len(values) == 0 {
		return "", false, err
	}
	return values[len(values)-1], true, nil
}

func (g *gitOperations) nativeConfigBool(
	ctx context.Context,
	key string,
) (bool, bool, error) {
	output, err := g.runGit(ctx, "", nil, "config", "--type=bool", "--get", key)
	if err != nil {
		if nativeGitCommandExitedWith(err, 1) {
			return false, false, nil
		}
		return false, false, err
	}
	return strings.TrimSpace(string(output)) == "true", true, nil
}

func (g *gitOperations) requireNonMirrorPushRemote(ctx context.Context, remote string) error {
	mirror, configured, err := g.nativeConfigBool(ctx, "remote."+remote+".mirror")
	if err != nil {
		return fmt.Errorf("failed to read mirror mode for remote %q: %w", remote, err)
	}
	if configured && mirror {
		return fmt.Errorf(
			"remote %q is configured as a mirror; refusing a push that may update multiple refs",
			remote,
		)
	}
	return nil
}

func (g *gitOperations) nativeConfigValues(ctx context.Context, key string) ([]string, error) {
	output, err := g.runGit(ctx, "", nil, "config", "--null", "--get-all", key)
	if err != nil {
		if nativeGitCommandExitedWith(err, 1) {
			return nil, nil
		}
		return nil, err
	}
	parts := strings.Split(string(output), "\x00")
	if len(parts) != 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts, nil
}

func (g *gitOperations) validateNativeTagRef(ctx context.Context, tagName string) (string, error) {
	tagRef := "refs/tags/" + tagName
	if _, err := g.runGit(ctx, "", nil, "check-ref-format", tagRef); err != nil {
		return "", fmt.Errorf("invalid tag name %q: %w", tagName, err)
	}
	return tagRef, nil
}

func (g *gitOperations) validateNativeHeadRef(ctx context.Context, ref string) error {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return fmt.Errorf("%q is not a branch ref", ref)
	}
	if _, err := g.runGit(ctx, "", nil, "check-ref-format", ref); err != nil {
		return fmt.Errorf("invalid branch ref %q: %w", ref, err)
	}
	return nil
}

func (g *gitOperations) nativeMergeRequestURL(
	ctx context.Context,
	target nativePushTarget,
) string {
	remoteURLs, err := g.nativeConfigValues(ctx, "remote."+target.remote+".url")
	if err != nil || len(remoteURLs) == 0 || remoteURLs[0] == "" {
		return ""
	}
	remoteInfo, err := parseRemoteURL(remoteURLs[0])
	if err != nil {
		return ""
	}
	effectivePushURLs, err := g.runGit(
		ctx,
		"",
		nil,
		"remote",
		"get-url",
		"--push",
		"--all",
		"--",
		target.remote,
	)
	if err != nil {
		return ""
	}
	for _, pushURL := range strings.Split(strings.TrimSuffix(string(effectivePushURLs), "\n"), "\n") {
		pushInfo, hosted := parseNativeHostedPushURL(pushURL)
		if hosted && !sameNativeRemoteRepository(remoteInfo, pushInfo) {
			return ""
		}
	}

	destination := strings.TrimPrefix(target.remoteRef, "refs/heads/")
	defaultBranch := g.nativeDefaultBranch(ctx, target.remote)
	if destination == defaultBranch {
		return ""
	}
	return generateMergeRequestURL(remoteInfo, destination, defaultBranch)
}

func parseNativeHostedPushURL(remoteURL string) (*RemoteInfo, bool) {
	if strings.HasPrefix(remoteURL, "file://") ||
		(len(remoteURL) >= 3 && remoteURL[1] == ':' &&
			(remoteURL[2] == '/' || remoteURL[2] == '\\')) {
		return nil, false
	}
	isHosted := strings.HasPrefix(remoteURL, "http://") ||
		strings.HasPrefix(remoteURL, "https://") ||
		strings.HasPrefix(remoteURL, "ssh://")
	if !isHosted {
		colon := strings.IndexByte(remoteURL, ':')
		slash := strings.IndexByte(remoteURL, '/')
		isHosted = colon > 0 && (slash == -1 || colon < slash)
	}
	if !isHosted {
		return nil, false
	}

	info, err := parseRemoteURL(remoteURL)
	if err != nil {
		return nil, false
	}
	return info, true
}

func sameNativeRemoteRepository(left, right *RemoteInfo) bool {
	return left != nil && right != nil &&
		strings.EqualFold(left.Host, right.Host) &&
		left.Owner == right.Owner &&
		left.Repo == right.Repo
}

func (g *gitOperations) nativeDefaultBranch(ctx context.Context, remote string) string {
	remoteHead := "refs/remotes/" + remote + "/HEAD"
	if _, err := g.runGit(ctx, "", nil, "check-ref-format", remoteHead); err != nil {
		return ""
	}
	output, err := g.runGit(ctx, "", nil, "symbolic-ref", "--quiet", remoteHead)
	if err != nil {
		return ""
	}
	prefix := "refs/remotes/" + remote + "/"
	ref := strings.TrimSuffix(string(output), "\n")
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimPrefix(ref, prefix)
}

func nativeGitCommandExitedWith(err error, code int) bool {
	var commandErr *gitCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	exitCode, ok := commandErr.ExitCode()
	return ok && exitCode == code
}

func nativeRemoteDispatchError(err error, operation string) error {
	var commandErr *gitCommandError
	if !errors.As(err, &commandErr) || !commandErr.dispatched {
		return nil
	}
	return &IndeterminateRemoteOutcomeError{Operation: operation, Cause: err}
}

func writeNativeTagMessage(message string) (string, func(), error) {
	file, err := os.CreateTemp("", "commit-tag-message-*")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	cleanup := func() {
		_ = os.Remove(path)
	}

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if _, err := file.WriteString(message); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

type nativeSemverTag struct {
	name  string
	parts [3]string
}

func parseNativeSemverTag(name string) (nativeSemverTag, bool) {
	if !strings.HasPrefix(name, "v") {
		return nativeSemverTag{}, false
	}
	parts := strings.Split(strings.TrimPrefix(name, "v"), ".")
	if len(parts) != 3 {
		return nativeSemverTag{}, false
	}

	var normalized [3]string
	for index, part := range parts {
		if part == "" {
			return nativeSemverTag{}, false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return nativeSemverTag{}, false
			}
		}
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
		normalized[index] = part
	}
	return nativeSemverTag{name: name, parts: normalized}, true
}

func compareNativeSemverTag(left, right nativeSemverTag) int {
	for index := range left.parts {
		if len(left.parts[index]) != len(right.parts[index]) {
			if len(left.parts[index]) > len(right.parts[index]) {
				return 1
			}
			return -1
		}
		if left.parts[index] > right.parts[index] {
			return 1
		}
		if left.parts[index] < right.parts[index] {
			return -1
		}
	}
	return 0
}
