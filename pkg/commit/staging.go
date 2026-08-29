package commit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hasansino/commit/pkg/commit/models"
)

type indexSnapshot struct {
	exists bool
	mode   os.FileMode
	mtime  time.Time
	data   []byte
}

// BeginStaging creates a private index, then stages into that index with native
// Git. The real index and HEAD are checked under their locks before the private
// session is returned.
func (g *gitOperations) BeginStaging(
	ctx context.Context,
	excludePatterns, includePatterns []string,
	useGlobalGitignore bool,
) (_ *models.StagingSessionState, retErr error) {
	excludeMatcher, err := newPathSelectorMatcher("exclude", excludePatterns)
	if err != nil {
		return nil, err
	}
	includeMatcher, err := newPathSelectorMatcher("include-only", includePatterns)
	if err != nil {
		return nil, err
	}

	if !g.sessionLease.TryLock() {
		return nil, fmt.Errorf("another staging session is already active")
	}
	leaseTransferred := false
	defer func() {
		if !leaseTransferred {
			g.sessionLease.Unlock()
		}
	}()

	indexPath, err := g.resolveGitPath(ctx, "index")
	if err != nil {
		return nil, err
	}

	var indexLock *os.File
	var headLocks []*os.File
	privateIndexPath := ""
	defer func() {
		if len(headLocks) != 0 {
			retErr = errors.Join(retErr, wrapHeadLockError(discardLocks(headLocks)))
		}
		if indexLock != nil {
			retErr = errors.Join(retErr, wrapIndexLockError(discardIndexLock(indexLock)))
		}
		if privateIndexPath != "" {
			retErr = errors.Join(retErr, cleanupPrivateIndex(privateIndexPath))
		}
	}()

	indexLock, err = acquireIndexLock(indexPath)
	if err != nil {
		return nil, err
	}
	expectedHead, err := g.snapshotHead(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot HEAD: %w", err)
	}
	headLocks, err = g.acquireHeadLocks(ctx, expectedHead)
	if err != nil {
		return nil, err
	}
	lockedHead, err := g.snapshotHead(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to verify locked HEAD: %w", err)
	}
	if lockedHead != expectedHead {
		return nil, errHeadChanged
	}
	if err := g.requireNormalRepositoryState(ctx); err != nil {
		return nil, err
	}

	baseTree, err := g.resolveBaseTree(ctx, expectedHead)
	if err != nil {
		return nil, err
	}
	originalIndex, err := snapshotIndex(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot git index: %w", err)
	}
	indexState, err := g.indexFingerprint(ctx, indexPath, baseTree)
	if err != nil {
		return nil, fmt.Errorf("failed to fingerprint git index: %w", err)
	}

	privateIndexPath, err = g.createPrivateIndex(ctx, indexPath, originalIndex)
	if err != nil {
		return nil, err
	}
	stagedFiles, intentToAddFiles, err := g.getStagedState(ctx, privateIndexPath, baseTree)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect staged files: %w", err)
	}
	if len(intentToAddFiles) != 0 {
		return nil, intentToAddError(intentToAddFiles)
	}
	usingExisting := len(stagedFiles) != 0

	if err := discardLocks(headLocks); err != nil {
		headLocks = nil
		return nil, wrapHeadLockError(err)
	}
	headLocks = nil
	if err := discardIndexLock(indexLock); err != nil {
		indexLock = nil
		return nil, wrapIndexLockError(err)
	}
	indexLock = nil

	if !usingExisting {
		if _, err := g.stageFilesAt(
			ctx,
			privateIndexPath,
			excludeMatcher,
			includeMatcher,
			useGlobalGitignore,
		); err != nil {
			return nil, fmt.Errorf("failed to stage files: %w", err)
		}
		stagedFiles, intentToAddFiles, err = g.getStagedState(ctx, privateIndexPath, baseTree)
		if err != nil {
			return nil, fmt.Errorf("failed to inspect staged files: %w", err)
		}
		if len(intentToAddFiles) != 0 {
			return nil, intentToAddError(intentToAddFiles)
		}
	}

	if len(stagedFiles) == 0 {
		if err := cleanupPrivateIndex(privateIndexPath); err != nil {
			return nil, err
		}
		privateIndexPath = ""
		state := &models.StagingSessionState{
			Phase:     models.StagingSessionNoChanges,
			Closed:    true,
			LeaseHeld: true,
		}
		g.activeSession = state
		leaseTransferred = true
		return state, nil
	}

	privateIndexState, err := g.indexFingerprint(ctx, privateIndexPath, baseTree)
	if err != nil {
		return nil, fmt.Errorf("failed to fingerprint private git index: %w", err)
	}

	// Staging ran without repository locks. Reacquire and verify every input
	// before publishing the private session.
	indexLock, err = acquireIndexLock(indexPath)
	if err != nil {
		return nil, err
	}
	headLocks, err = g.acquireHeadLocks(ctx, expectedHead)
	if err != nil {
		return nil, err
	}
	if err := g.verifyRepositoryInputs(
		ctx,
		indexPath,
		privateIndexPath,
		baseTree,
		expectedHead,
		indexState,
		originalIndex.rawState(),
		privateIndexState,
	); err != nil {
		return nil, err
	}
	if err := discardLocks(headLocks); err != nil {
		headLocks = nil
		return nil, wrapHeadLockError(err)
	}
	headLocks = nil
	if err := discardIndexLock(indexLock); err != nil {
		indexLock = nil
		return nil, wrapIndexLockError(err)
	}
	indexLock = nil

	mode := os.FileMode(0o600)
	if originalIndex.exists {
		mode = originalIndex.mode
	}
	state := &models.StagingSessionState{
		Files:             append([]string(nil), stagedFiles...),
		IndexPath:         indexPath,
		PrivateIndexPath:  privateIndexPath,
		IndexMode:         mode,
		IndexState:        indexState,
		RawIndexState:     originalIndex.rawState(),
		PrivateIndexState: privateIndexState,
		BaseTree:          baseTree,
		ExpectedHead:      expectedHead,
		UsingExisting:     usingExisting,
		Phase:             models.StagingSessionPrepared,
		LeaseHeld:         true,
	}
	privateIndexPath = ""
	g.activeSession = state
	leaseTransferred = true
	return state, nil
}

// FinishStaging consumes a staging session. Recovery artifacts are deliberately
// retained when CreateCommit could not determine a safe automatic outcome.
func (g *gitOperations) FinishStaging(session *models.StagingSessionState) error {
	if err := g.validateStagingSession(session); err != nil {
		return err
	}
	state := session
	defer func() {
		state.Closed = true
		g.releaseStagingLease(state)
	}()

	if state.Phase == models.StagingSessionFinished {
		return nil
	}
	if state.Phase == models.StagingSessionRecoveryRequired {
		if state.Recovery != nil {
			return state.Recovery
		}
		return fmt.Errorf("commit recovery is required; staging artifacts were retained")
	}
	if state.Phase == models.StagingSessionCommitting {
		return fmt.Errorf("staging session is still being committed")
	}
	if state.PrivateIndexPath != "" {
		if err := cleanupPrivateIndex(state.PrivateIndexPath); err != nil {
			return err
		}
		state.PrivateIndexPath = ""
	}
	state.Phase = models.StagingSessionFinished
	return nil
}

func (g *gitOperations) releaseStagingLease(state *models.StagingSessionState) {
	if state.LeaseHeld {
		state.LeaseHeld = false
		g.sessionLease.Unlock()
	}
}

func (g *gitOperations) validateStagingSession(
	session *models.StagingSessionState,
) error {
	if session == nil {
		return fmt.Errorf("staging session is nil")
	}
	if session != g.activeSession {
		return fmt.Errorf("staging session belongs to another repository")
	}
	return nil
}

func (g *gitOperations) validatePreparedStagingSession(
	session *models.StagingSessionState,
) error {
	if err := g.validateStagingSession(session); err != nil {
		return err
	}
	if session.Phase != models.StagingSessionPrepared || session.Closed {
		return fmt.Errorf("staging session is already closed")
	}
	if session.PrivateIndexPath == "" {
		return fmt.Errorf("staging session has no private index")
	}
	return nil
}

func (g *gitOperations) verifyRepositoryInputs(
	ctx context.Context,
	indexPath, privateIndexPath, baseTree string,
	expectedHead models.HeadSnapshot,
	expectedIndexState []byte,
	expectedRawIndexState models.RawIndexState,
	expectedPrivateIndexState []byte,
) error {
	currentHead, err := g.snapshotHead(ctx)
	if err != nil {
		return fmt.Errorf("failed to verify HEAD: %w", err)
	}
	if currentHead != expectedHead {
		return errHeadChanged
	}
	if err := g.requireNormalRepositoryState(ctx); err != nil {
		return err
	}
	currentIndexState, err := g.indexFingerprint(ctx, indexPath, baseTree)
	if err != nil {
		return fmt.Errorf("failed to verify git index: %w", err)
	}
	if !bytes.Equal(currentIndexState, expectedIndexState) {
		return errIndexChanged
	}
	currentIndex, err := snapshotIndex(indexPath)
	if err != nil {
		return fmt.Errorf("failed to snapshot git index: %w", err)
	}
	if currentIndex.rawState() != expectedRawIndexState {
		return errIndexChanged
	}
	currentPrivateIndexState, err := g.indexFingerprint(ctx, privateIndexPath, baseTree)
	if err != nil {
		return fmt.Errorf("failed to verify private git index: %w", err)
	}
	if !bytes.Equal(currentPrivateIndexState, expectedPrivateIndexState) {
		return errPrivateIndexChanged
	}
	return nil
}

var (
	errHeadChanged = errors.New(
		"HEAD changed while preparing the commit; rerun the command",
	)
	errIndexChanged = errors.New(
		"git index changed while preparing the commit; rerun the command",
	)
	errPrivateIndexChanged = errors.New(
		"private git index changed while preparing the commit; rerun the command",
	)
)

func (g *gitOperations) resolveGitPath(ctx context.Context, name string) (string, error) {
	output, err := g.runGit(ctx, "", nil, "rev-parse", "--git-path", name)
	if err != nil {
		return "", fmt.Errorf("failed to resolve git path %q: %w", name, err)
	}
	resolvedPath := strings.TrimSpace(string(output))
	if resolvedPath == "" {
		return "", fmt.Errorf("git returned an empty path for %q", name)
	}
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(g.repoPath, resolvedPath)
	}
	return filepath.Clean(resolvedPath), nil
}

func (g *gitOperations) snapshotHead(ctx context.Context) (models.HeadSnapshot, error) {
	name := "HEAD"
	nameOutput, err := g.runGit(ctx, "", nil, "symbolic-ref", "-q", "HEAD")
	if err == nil {
		name = strings.TrimSpace(string(nameOutput))
	} else if !gitCommandExitedWith(err, 1) {
		return models.HeadSnapshot{}, fmt.Errorf("failed to resolve HEAD reference: %w", err)
	}

	hashOutput, err := g.runGit(ctx, "", nil, "rev-parse", "--verify", "HEAD")
	if err == nil {
		return models.HeadSnapshot{Name: name, Hash: strings.TrimSpace(string(hashOutput))}, nil
	}
	if name == "HEAD" {
		return models.HeadSnapshot{}, fmt.Errorf("failed to resolve HEAD commit: %w", err)
	}
	_, verifyErr := g.runGit(ctx, "", nil, "show-ref", "--verify", "--quiet", name)
	if gitCommandExitedWith(verifyErr, 1) {
		return models.HeadSnapshot{Name: name}, nil
	}
	if verifyErr != nil {
		return models.HeadSnapshot{}, fmt.Errorf("failed to verify unborn HEAD: %w", verifyErr)
	}
	return models.HeadSnapshot{}, fmt.Errorf("HEAD reference %q exists but cannot be resolved", name)
}

func (g *gitOperations) resolveBaseTree(ctx context.Context, head models.HeadSnapshot) (string, error) {
	if head.Hash != "" {
		return head.Hash, nil
	}
	output, err := g.runGit(
		ctx,
		"",
		strings.NewReader(""),
		"hash-object",
		"-t",
		"tree",
		"-w",
		"--stdin",
	)
	if err != nil {
		return "", fmt.Errorf("failed to create empty base tree: %w", err)
	}
	baseTree := strings.TrimSpace(string(output))
	if baseTree == "" {
		return "", fmt.Errorf("git returned an empty tree hash")
	}
	return baseTree, nil
}

func (g *gitOperations) getStagedState(
	ctx context.Context,
	indexPath, baseTree string,
) ([]string, []string, error) {
	stagedFiles, err := g.diffNames(ctx, indexPath, baseTree, "--ita-invisible-in-index")
	if err != nil {
		return nil, nil, err
	}
	visibleFiles, err := g.diffNames(ctx, indexPath, baseTree, "--ita-visible-in-index")
	if err != nil {
		return nil, nil, err
	}
	stagedSet := make(map[string]struct{}, len(stagedFiles))
	for _, file := range stagedFiles {
		stagedSet[file] = struct{}{}
	}
	intentToAddFiles := make([]string, 0)
	for _, file := range visibleFiles {
		if _, ok := stagedSet[file]; !ok {
			intentToAddFiles = append(intentToAddFiles, file)
		}
	}
	return stagedFiles, intentToAddFiles, nil
}

func (g *gitOperations) diffNames(
	ctx context.Context,
	indexPath, baseTree, intentToAddOption string,
) ([]string, error) {
	output, err := g.runGit(
		ctx,
		indexPath,
		nil,
		"--literal-pathspecs",
		"diff",
		"--cached",
		"--name-only",
		"-z",
		"--no-renames",
		intentToAddOption,
		baseTree,
		"--",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect staged names: %w", err)
	}
	return splitNullTerminated(output), nil
}

// indexFingerprint records user-visible entries and flags while ignoring
// refreshable stat/cache metadata.
func (g *gitOperations) indexFingerprint(
	ctx context.Context,
	indexPath, baseTree string,
) ([]byte, error) {
	commands := [][]string{
		{"--literal-pathspecs", "ls-files", "--stage", "--full-name", "-z", "--"},
		{"--literal-pathspecs", "ls-files", "-v", "--full-name", "-z", "--"},
		{"--literal-pathspecs", "ls-files", "--resolve-undo", "--full-name", "-z", "--"},
		{
			"--literal-pathspecs", "diff", "--cached", "--raw", "-z", "--no-renames",
			"--ita-invisible-in-index", baseTree, "--",
		},
		{
			"--literal-pathspecs", "diff", "--cached", "--raw", "-z", "--no-renames",
			"--ita-visible-in-index", baseTree, "--",
		},
	}
	var fingerprint []byte
	for _, args := range commands {
		output, err := g.runGit(ctx, indexPath, nil, args...)
		if err != nil {
			return nil, err
		}
		fingerprint = binary.BigEndian.AppendUint64(fingerprint, uint64(len(output)))
		fingerprint = append(fingerprint, output...)
	}
	return fingerprint, nil
}

func (g *gitOperations) createPrivateIndex(
	ctx context.Context,
	indexPath string,
	original indexSnapshot,
) (_ string, retErr error) {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create private index directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(indexPath), ".commit-index-*")
	if err != nil {
		return "", fmt.Errorf("failed to create private git index: %w", err)
	}
	path := file.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, file.Close())
		}
		if retErr != nil {
			retErr = errors.Join(retErr, cleanupPrivateIndex(path))
		}
	}()

	if original.exists {
		if err := file.Chmod(0o600); err != nil {
			return "", err
		}
		if _, err := io.Copy(file, bytes.NewReader(original.data)); err != nil {
			return "", err
		}
		if err := file.Sync(); err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		closed = true
		// Git compares the cached entry timestamps with the index file's
		// timestamp to detect "racy clean" files. A byte-for-byte copy with a
		// newer mtime can make an immediately rewritten, same-size worktree file
		// look unchanged. Preserve the source index mtime so native diff-files
		// retains Git's normal content-check behavior.
		if err := os.Chtimes(path, original.mtime, original.mtime); err != nil {
			return "", fmt.Errorf("failed to preserve private git index timestamp: %w", err)
		}
		return path, nil
	}

	if err := file.Close(); err != nil {
		return "", err
	}
	closed = true
	if err := os.Remove(path); err != nil {
		return "", err
	}
	if _, err := g.runGit(ctx, path, nil, "read-tree", "--empty"); err != nil {
		return "", fmt.Errorf("failed to initialize private git index: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("failed to protect private git index: %w", err)
	}
	return path, nil
}

func snapshotIndex(indexPath string) (indexSnapshot, error) {
	info, err := os.Stat(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return indexSnapshot{}, nil
		}
		return indexSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return indexSnapshot{}, fmt.Errorf("git index is not a regular file")
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return indexSnapshot{}, err
	}
	return indexSnapshot{
		exists: true,
		mode:   info.Mode(),
		mtime:  info.ModTime(),
		data:   data,
	}, nil
}

func (s indexSnapshot) rawState() models.RawIndexState {
	return models.RawIndexState{
		Exists: s.exists,
		Mode:   s.mode,
		Digest: sha256.Sum256(s.data),
	}
}

func acquireIndexLock(indexPath string) (*os.File, error) {
	return acquirePathLock(indexPath, "git index")
}

func (g *gitOperations) acquireHeadLocks(
	ctx context.Context,
	head models.HeadSnapshot,
) ([]*os.File, error) {
	names := []string{"HEAD"}
	if head.Name != "HEAD" {
		names = append(names, head.Name)
	}
	locks := make([]*os.File, 0, len(names))
	for _, name := range names {
		path, err := g.resolveGitPath(ctx, name)
		if err != nil {
			return nil, errors.Join(err, wrapHeadLockError(discardLocks(locks)))
		}
		lock, err := acquirePathLock(path, "git reference")
		if err != nil {
			return nil, errors.Join(err, wrapHeadLockError(discardLocks(locks)))
		}
		locks = append(locks, lock)
	}
	return locks, nil
}

func acquirePathLock(path, description string) (*os.File, error) {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create %s lock directory: %w", description, err)
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%s is locked by another process: %s", description, lockPath)
		}
		return nil, fmt.Errorf("failed to lock %s: %w", description, err)
	}
	return lock, nil
}

func discardLocks(locks []*os.File) error {
	var errs []error
	for index := len(locks) - 1; index >= 0; index-- {
		errs = append(errs, discardIndexLock(locks[index]))
	}
	return errors.Join(errs...)
}

func discardIndexLock(indexLock *os.File) error {
	if indexLock == nil {
		return nil
	}
	lockPath := indexLock.Name()
	closeErr := indexLock.Close()
	removeErr := os.Remove(lockPath)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

func cleanupPrivateIndex(path string) error {
	if path == "" {
		return nil
	}
	var errs []error
	for _, candidate := range []string{path + ".lock", path} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("failed to remove %s: %w", candidate, err))
		}
	}
	return errors.Join(errs...)
}

func writeIndexSnapshotToOpenLock(lock *os.File, snapshot indexSnapshot) error {
	if !snapshot.exists {
		return fmt.Errorf("cannot write a missing index snapshot")
	}
	if err := lock.Truncate(0); err != nil {
		return err
	}
	if _, err := lock.Seek(0, 0); err != nil {
		return err
	}
	if err := lock.Chmod(snapshot.mode.Perm()); err != nil {
		return err
	}
	if _, err := io.Copy(lock, bytes.NewReader(snapshot.data)); err != nil {
		return err
	}
	return lock.Sync()
}

func (g *gitOperations) requireNormalRepositoryState(ctx context.Context) error {
	for _, marker := range repoStateMarkers {
		for _, name := range marker.paths {
			path, err := g.resolveGitPath(ctx, name)
			if err != nil {
				return fmt.Errorf("failed to resolve repository state marker %q: %w", name, err)
			}
			_, err = os.Stat(path)
			switch {
			case err == nil:
				return fmt.Errorf("cannot create a commit while repository state is %s", marker.state)
			case os.IsNotExist(err):
				continue
			default:
				return fmt.Errorf("failed to inspect repository state marker %q: %w", name, err)
			}
		}
	}
	return nil
}

func intentToAddError(files []string) error {
	return fmt.Errorf(
		"intent-to-add entries are not supported safely: %q; fully stage or reset them and rerun",
		files,
	)
}

func gitCommandExitedWith(err error, code int) bool {
	var commandErr *gitCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	exitCode, ok := commandErr.ExitCode()
	return ok && exitCode == code
}

func wrapIndexLockError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to release git index lock: %w", err)
}

func wrapHeadLockError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to release git reference locks: %w", err)
}
