package commit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hasansino/commit/pkg/commit/models"
)

var nativeDiffContextLevels = []int{5, 3, 2, 1, 0}

func (g *gitOperations) stageFilesAt(
	ctx context.Context,
	indexPath string,
	excludeMatcher, includeMatcher *pathSelectorMatcher,
	useGlobalGitignore bool,
) (_ []string, retErr error) {
	prefix := make([]string, 0, 3)
	if !useGlobalGitignore {
		emptyExcludes, err := os.CreateTemp(filepath.Dir(indexPath), ".commit-excludes-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create empty global excludes file: %w", err)
		}
		emptyExcludesPath := emptyExcludes.Name()
		if err := emptyExcludes.Close(); err != nil {
			_ = os.Remove(emptyExcludesPath)
			return nil, fmt.Errorf("failed to close empty global excludes file: %w", err)
		}
		defer func() {
			if err := os.Remove(emptyExcludesPath); err != nil && !os.IsNotExist(err) {
				retErr = errors.Join(
					retErr,
					fmt.Errorf("failed to remove empty global excludes file: %w", err),
				)
			}
		}()
		prefix = append(prefix, "-c", "core.excludesFile="+emptyExcludesPath)
	}
	prefix = append(prefix, "--literal-pathspecs")

	trackedArgs := append(
		append([]string(nil), prefix...),
		"diff-files", "--name-only", "-z", "--no-renames", "--",
	)
	trackedOutput, err := g.runGit(ctx, indexPath, nil, trackedArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to discover tracked working-tree changes: %w", err)
	}
	untrackedArgs := append(
		append([]string(nil), prefix...),
		"ls-files", "--others", "--exclude-standard", "-z", "--",
	)
	untrackedOutput, err := g.runGit(ctx, indexPath, nil, untrackedArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to discover untracked files: %w", err)
	}

	candidates := make(map[string]struct{})
	for _, file := range append(
		splitNullTerminated(trackedOutput),
		splitNullTerminated(untrackedOutput)...,
	) {
		if excludeMatcher.Match(file) {
			continue
		}
		if includeMatcher != nil && !includeMatcher.Match(file) {
			continue
		}
		candidates[file] = struct{}{}
	}

	files := make([]string, 0, len(candidates))
	for file := range candidates {
		files = append(files, file)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return files, nil
	}

	var pathspec bytes.Buffer
	for _, file := range files {
		pathspec.WriteString(file)
		pathspec.WriteByte(0)
	}
	addArgs := append(
		append([]string(nil), prefix...),
		"add", "-A", "--pathspec-from-file=-", "--pathspec-file-nul",
	)
	if _, err := g.runGit(ctx, indexPath, &pathspec, addArgs...); err != nil {
		return nil, fmt.Errorf("failed to stage selected files: %w", err)
	}
	// Git creates an alternate index through its own lock file and may replace
	// our original 0600 mode with the process umask's default.
	if err := os.Chmod(indexPath, 0o600); err != nil {
		return nil, fmt.Errorf("failed to protect private git index: %w", err)
	}
	return files, nil
}

func (g *gitOperations) GetStagedDiff(
	ctx context.Context,
	session *models.StagingSessionState,
	maxSizeBytes int,
) (string, error) {
	if maxSizeBytes <= 0 {
		return "", fmt.Errorf("maximum diff size must be greater than zero")
	}
	if err := g.validateStagingSession(session); err != nil {
		return "", err
	}
	if session.Phase == models.StagingSessionNoChanges {
		return "", nil
	}
	if err := g.validatePreparedStagingSession(session); err != nil {
		return "", err
	}
	state := session
	currentState, err := g.indexFingerprint(ctx, state.PrivateIndexPath, state.BaseTree)
	if err != nil {
		return "", fmt.Errorf("failed to verify private git index: %w", err)
	}
	if !bytes.Equal(currentState, state.PrivateIndexState) {
		return "", errPrivateIndexChanged
	}

	baseArgs := []string{
		"--literal-pathspecs",
		"diff",
		"--cached",
		"--no-color",
		"--no-ext-diff",
		"--no-prefix",
		"--diff-algorithm=patience",
		"--find-renames=50%",
		state.BaseTree,
	}
	var diff []byte
	for _, contextLevel := range nativeDiffContextLevels {
		args := append([]string(nil), baseArgs...)
		args = append(args, fmt.Sprintf("-U%d", contextLevel), "--")
		diff, err = g.runGit(ctx, state.PrivateIndexPath, nil, args...)
		if err != nil {
			return "", fmt.Errorf("failed to get staged diff: %w", err)
		}
		if len(diff) <= maxSizeBytes {
			return compactUnifiedDiff(string(diff), maxSizeBytes)
		}
	}
	return compactUnifiedDiff(string(diff), maxSizeBytes)
}

func splitNullTerminated(output []byte) []string {
	parts := bytes.Split(output, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			result = append(result, string(part))
		}
	}
	return result
}
