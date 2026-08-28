package commit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type indexSnapshot struct {
	exists bool
	mode   os.FileMode
	data   []byte
}

type headSnapshot struct {
	name string
	hash string
}

// stagingSession belongs to one Execute invocation and is not shared between
// goroutines. Filesystem locks coordinate it with other Git processes.
type stagingSession struct {
	owner         *gitOperations
	files         []string
	indexPath     string
	rollbackIndex *indexSnapshot
	indexState    []byte
	expectedHead  headSnapshot
	closed        bool
	leaseHeld     bool
}

func (s *stagingSession) usesExistingStaging() bool {
	return len(s.files) > 0 && s.rollbackIndex == nil
}

// BeginStaging preserves an existing staged set. If the index has no staged
// changes, it records an exact restore point before staging the requested
// working-tree changes.
func (g *gitOperations) BeginStaging(
	excludePatterns, includePatterns []string,
	useGlobalGitignore bool,
) (_ *stagingSession, retErr error) {
	if !g.sessionLease.TryLock() {
		return nil, fmt.Errorf("another staging session is already active")
	}
	leaseTransferred := false
	defer func() {
		if !leaseTransferred {
			g.sessionLease.Unlock()
		}
	}()

	indexPath, err := g.resolveIndexPath()
	if err != nil {
		return nil, err
	}

	indexLock, err := acquireIndexLock(indexPath)
	if err != nil {
		return nil, err
	}
	lockOwned := true
	defer func() {
		if lockOwned {
			retErr = errors.Join(
				retErr,
				wrapIndexLockError(discardIndexLock(indexLock)),
			)
		}
	}()

	originalIndex, err := snapshotIndex(indexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot git index: %w", err)
	}

	expectedHead, err := g.snapshotHead()
	if err != nil {
		return nil, fmt.Errorf("failed to snapshot HEAD: %w", err)
	}
	headLocks, err := g.acquireHeadLocks(expectedHead)
	if err != nil {
		return nil, err
	}
	headLocksOwned := true
	defer func() {
		if headLocksOwned {
			retErr = errors.Join(
				retErr,
				wrapHeadLockError(discardLocks(headLocks)),
			)
		}
	}()

	// HEAD can move between the initial snapshot and acquiring its locks.
	// Revalidate while both the index and references are locked so the
	// speculative index is always based on one coherent repository state.
	lockedHead, err := g.snapshotHead()
	if err != nil {
		return nil, fmt.Errorf("failed to verify locked HEAD: %w", err)
	}
	if lockedHead != expectedHead {
		return nil, errHeadChanged
	}

	stagedFiles, intentToAddFiles, err := g.getStagedState()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect staged files: %w", err)
	}
	if len(intentToAddFiles) > 0 {
		return nil, intentToAddError(intentToAddFiles)
	}

	if len(stagedFiles) > 0 {
		indexState, err := g.indexFingerprint()
		if err != nil {
			return nil, fmt.Errorf("failed to fingerprint git index: %w", err)
		}
		prepared := &stagingSession{
			owner:        g,
			files:        stagedFiles,
			indexPath:    indexPath,
			indexState:   indexState,
			expectedHead: expectedHead,
		}

		headReleaseErr := discardLocks(headLocks)
		headLocksOwned = false
		if headReleaseErr != nil {
			return nil, wrapHeadLockError(headReleaseErr)
		}
		releaseErr := discardIndexLock(indexLock)
		lockOwned = false
		if releaseErr != nil {
			return nil, fmt.Errorf("failed to release git index lock: %w", releaseErr)
		}
		prepared.leaseHeld = true
		leaseTransferred = true
		return prepared, nil
	}

	if _, err := g.StageFiles(excludePatterns, includePatterns, useGlobalGitignore); err != nil {
		return nil, g.restoreAfterStagingError(
			indexLock,
			indexPath,
			originalIndex,
			fmt.Errorf("failed to stage files: %w", err),
			&lockOwned,
		)
	}

	stagedFiles, intentToAddFiles, err = g.getStagedState()
	if err != nil {
		return nil, g.restoreAfterStagingError(
			indexLock,
			indexPath,
			originalIndex,
			fmt.Errorf("failed to inspect staged files: %w", err),
			&lockOwned,
		)
	}
	if len(intentToAddFiles) > 0 {
		return nil, g.restoreAfterStagingError(
			indexLock,
			indexPath,
			originalIndex,
			intentToAddError(intentToAddFiles),
			&lockOwned,
		)
	}
	if len(stagedFiles) == 0 {
		restoreErr := restoreIndexWithLock(indexLock, indexPath, originalIndex)
		lockOwned = false
		headReleaseErr := discardLocks(headLocks)
		headLocksOwned = false
		if err := errors.Join(
			wrapRestoreError(restoreErr),
			wrapHeadLockError(headReleaseErr),
		); err != nil {
			return nil, err
		}
		prepared := &stagingSession{owner: g, closed: true, leaseHeld: true}
		leaseTransferred = true
		return prepared, nil
	}

	indexState, err := g.indexFingerprint()
	if err != nil {
		return nil, g.restoreAfterStagingError(
			indexLock,
			indexPath,
			originalIndex,
			fmt.Errorf("failed to fingerprint staged git index: %w", err),
			&lockOwned,
		)
	}
	prepared := &stagingSession{
		owner:         g,
		files:         stagedFiles,
		indexPath:     indexPath,
		rollbackIndex: &originalIndex,
		indexState:    indexState,
		expectedHead:  expectedHead,
	}

	headReleaseErr := discardLocks(headLocks)
	headLocksOwned = false
	if headReleaseErr != nil {
		restoreErr := restoreIndexWithLock(indexLock, indexPath, originalIndex)
		lockOwned = false
		return nil, errors.Join(
			wrapHeadLockError(headReleaseErr),
			wrapRestoreError(restoreErr),
		)
	}

	releaseErr := discardIndexLock(indexLock)
	lockOwned = false
	if releaseErr != nil {
		restoreErr := restoreAfterLockReleaseFailure(indexPath, originalIndex, releaseErr)
		return nil, errors.Join(
			fmt.Errorf("failed to release git index lock: %w", releaseErr),
			wrapRestoreError(restoreErr),
		)
	}

	prepared.leaseHeld = true
	leaseTransferred = true
	return prepared, nil
}

// FinishStaging consumes one prepared staging session. If CreateCommit did not
// consume it, staging performed by this session is rolled back. The session is
// terminal even when cleanup fails, so an error can never poison later runs.
func (g *gitOperations) FinishStaging(session *stagingSession) error {
	if err := g.validateStagingSession(session); err != nil {
		return err
	}
	defer func() {
		session.closed = true
		g.releaseStagingLease(session)
	}()

	if session.closed {
		return nil
	}
	if session.rollbackIndex == nil {
		return nil
	}

	indexLock, err := acquireIndexLock(session.indexPath)
	if err != nil {
		return err
	}
	headLocks, err := g.acquireHeadLocks(session.expectedHead)
	if err != nil {
		return errors.Join(err, wrapIndexLockError(discardIndexLock(indexLock)))
	}

	if err := g.verifyStagingSession(session); err != nil {
		headReleaseErr := discardLocks(headLocks)
		releaseErr := discardIndexLock(indexLock)
		return errors.Join(
			err,
			wrapHeadLockError(headReleaseErr),
			wrapIndexLockError(releaseErr),
		)
	}

	if err := restoreIndexWithLock(indexLock, session.indexPath, *session.rollbackIndex); err != nil {
		return errors.Join(
			fmt.Errorf("failed to restore git index: %w", err),
			wrapHeadLockError(discardLocks(headLocks)),
		)
	}

	headReleaseErr := discardLocks(headLocks)
	return wrapHeadLockError(headReleaseErr)
}

func (g *gitOperations) releaseStagingLease(session *stagingSession) {
	if !session.leaseHeld {
		return
	}
	session.leaseHeld = false
	g.sessionLease.Unlock()
}

// lockAndVerifyStaging prevents native Git from changing the index between the
// final verification and go-git's commit-tree construction.
func (g *gitOperations) lockAndVerifyStaging(
	session *stagingSession,
) (func() error, error) {
	if err := g.validateStagingSession(session); err != nil {
		return nil, err
	}

	if session.closed {
		return nil, fmt.Errorf("staging session is already closed")
	}

	indexLock, err := acquireIndexLock(session.indexPath)
	if err != nil {
		return nil, err
	}
	headLocks, err := g.acquireHeadLocks(session.expectedHead)
	if err != nil {
		releaseErr := discardIndexLock(indexLock)
		return nil, errors.Join(err, wrapIndexLockError(releaseErr))
	}

	if err := g.verifyStagingSession(session); err != nil {
		headReleaseErr := discardLocks(headLocks)
		releaseErr := discardIndexLock(indexLock)
		if errors.Is(err, errHeadChanged) || errors.Is(err, errIndexChanged) {
			session.closed = true
		}
		return nil, errors.Join(
			err,
			wrapHeadLockError(headReleaseErr),
			wrapIndexLockError(releaseErr),
		)
	}

	return func() error {
		headReleaseErr := discardLocks(headLocks)
		indexReleaseErr := discardIndexLock(indexLock)
		return errors.Join(
			wrapHeadLockError(headReleaseErr),
			wrapIndexLockError(indexReleaseErr),
		)
	}, nil
}

func (g *gitOperations) validateStagingSession(session *stagingSession) error {
	if session == nil {
		return fmt.Errorf("staging session is nil")
	}
	if session.owner != g {
		return fmt.Errorf("staging session belongs to another repository")
	}
	return nil
}

func (g *gitOperations) verifyStagingSession(session *stagingSession) error {
	currentIndexState, err := g.indexFingerprint()
	if err != nil {
		return fmt.Errorf("failed to verify git index: %w", err)
	}
	if !bytes.Equal(currentIndexState, session.indexState) {
		return errIndexChanged
	}

	currentHead, err := g.snapshotHead()
	if err != nil {
		return fmt.Errorf("failed to verify HEAD: %w", err)
	}
	if currentHead != session.expectedHead {
		return errHeadChanged
	}

	return nil
}

var (
	errHeadChanged  = errors.New("HEAD changed while preparing the commit; rerun the command")
	errIndexChanged = errors.New("git index changed while preparing the commit; rerun the command")
)

func (g *gitOperations) restoreAfterStagingError(
	indexLock *os.File,
	indexPath string,
	originalIndex indexSnapshot,
	cause error,
	lockOwned *bool,
) error {
	restoreErr := restoreIndexWithLock(indexLock, indexPath, originalIndex)
	*lockOwned = false
	if restoreErr == nil {
		return cause
	}
	return errors.Join(cause, fmt.Errorf("failed to restore git index: %w", restoreErr))
}

func (g *gitOperations) resolveIndexPath() (string, error) {
	return g.resolveGitPath("index")
}

func (g *gitOperations) resolveGitPath(name string) (string, error) {
	output, err := g.gitCommand("rev-parse", "--git-path", name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"failed to resolve git path %q: %w: %s",
			name,
			err,
			strings.TrimSpace(string(output)),
		)
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

func (g *gitOperations) snapshotHead() (headSnapshot, error) {
	name := "HEAD"
	nameOutput, err := g.gitCommand("symbolic-ref", "-q", "HEAD").CombinedOutput()
	if err == nil {
		name = strings.TrimSpace(string(nameOutput))
	} else if !commandExitedWith(err, 1) {
		return headSnapshot{}, fmt.Errorf(
			"failed to resolve HEAD reference: %w: %s",
			err,
			strings.TrimSpace(string(nameOutput)),
		)
	}

	hashOutput, err := g.gitCommand("rev-parse", "--verify", "HEAD").CombinedOutput()
	if err != nil {
		if name != "HEAD" {
			verifyErr := g.gitCommand("show-ref", "--verify", "--quiet", name).Run()
			if commandExitedWith(verifyErr, 1) {
				return headSnapshot{name: name}, nil
			}
		}
		return headSnapshot{}, fmt.Errorf(
			"failed to resolve HEAD commit: %w: %s",
			err,
			strings.TrimSpace(string(hashOutput)),
		)
	}

	return headSnapshot{
		name: name,
		hash: strings.TrimSpace(string(hashOutput)),
	}, nil
}

func commandExitedWith(err error, code int) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == code
}

func (g *gitOperations) getStagedState() ([]string, []string, error) {
	stagedFiles, err := g.diffNames("--ita-invisible-in-index")
	if err != nil {
		return nil, nil, err
	}

	visibleFiles, err := g.diffNames("--ita-visible-in-index")
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

func (g *gitOperations) diffNames(intentToAddOption string) ([]string, error) {
	output, err := g.gitCommand(
		"diff",
		"--cached",
		"--name-only",
		"-z",
		"--no-renames",
		intentToAddOption,
		"--",
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	parts := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			files = append(files, string(part))
		}
	}
	return files, nil
}

// indexFingerprint captures user-visible index entries and flags while
// intentionally ignoring refreshable cache/stat metadata. That lets rollback
// coexist with background `git status` calls without overwriting real staging
// changes made by another process.
func (g *gitOperations) indexFingerprint() ([]byte, error) {
	commands := [][]string{
		{"--literal-pathspecs", "ls-files", "--stage", "--full-name", "-z", "--"},
		{"--literal-pathspecs", "ls-files", "-v", "--full-name", "-z", "--"},
		{"--literal-pathspecs", "ls-files", "--resolve-undo", "--full-name", "-z", "--"},
		{
			"--literal-pathspecs", "diff", "--cached", "--raw", "-z", "--no-renames",
			"--ita-invisible-in-index", "--",
		},
		{
			"--literal-pathspecs", "diff", "--cached", "--raw", "-z", "--no-renames",
			"--ita-visible-in-index", "--",
		},
	}

	var fingerprint []byte
	for _, args := range commands {
		output, err := g.gitCommand(args...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf(
				"git %s failed: %w: %s",
				strings.Join(args, " "),
				err,
				strings.TrimSpace(string(output)),
			)
		}
		fingerprint = binary.BigEndian.AppendUint64(fingerprint, uint64(len(output)))
		fingerprint = append(fingerprint, output...)
	}

	return fingerprint, nil
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
	return indexSnapshot{exists: true, mode: info.Mode(), data: data}, nil
}

func acquireIndexLock(indexPath string) (*os.File, error) {
	return acquirePathLock(indexPath, "git index")
}

func (g *gitOperations) acquireHeadLocks(head headSnapshot) ([]*os.File, error) {
	names := []string{"HEAD"}
	if head.name != "HEAD" {
		names = append(names, head.name)
	}

	locks := make([]*os.File, 0, len(names))
	for _, name := range names {
		path, err := g.resolveGitPath(name)
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

type lockReleaseError struct {
	path  string
	info  os.FileInfo
	cause error
}

func (e *lockReleaseError) Error() string {
	return e.cause.Error()
}

func (e *lockReleaseError) Unwrap() error {
	return e.cause
}

func discardIndexLock(indexLock *os.File) error {
	if indexLock == nil {
		return nil
	}
	lockPath := indexLock.Name()
	lockInfo, statErr := indexLock.Stat()
	closeErr := indexLock.Close()
	removeErr := os.Remove(lockPath)
	if removeErr == nil || os.IsNotExist(removeErr) {
		// The lock no longer excludes another process. A close error on an
		// empty sentinel file cannot affect repository state.
		return nil
	}
	return &lockReleaseError{
		path:  lockPath,
		info:  lockInfo,
		cause: errors.Join(statErr, closeErr, removeErr),
	}
}

func restoreAfterLockReleaseFailure(
	indexPath string,
	snapshot indexSnapshot,
	releaseErr error,
) error {
	var ownedLock *lockReleaseError
	if !errors.As(releaseErr, &ownedLock) || ownedLock.info == nil {
		return fmt.Errorf("cannot prove ownership of the unreleased git index lock")
	}
	if filepath.Clean(ownedLock.path) != filepath.Clean(indexPath+".lock") {
		return fmt.Errorf("unreleased lock does not belong to the git index")
	}

	currentInfo, err := os.Lstat(ownedLock.path)
	if err != nil {
		return fmt.Errorf("failed to inspect unreleased git index lock: %w", err)
	}
	if !os.SameFile(ownedLock.info, currentInfo) {
		return fmt.Errorf("git index lock was replaced by another process")
	}

	indexLock, err := os.OpenFile(ownedLock.path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to reopen git index lock for restoration: %w", err)
	}
	openedInfo, err := indexLock.Stat()
	if err != nil {
		_ = indexLock.Close()
		return fmt.Errorf("failed to verify reopened git index lock: %w", err)
	}
	if !os.SameFile(ownedLock.info, openedInfo) {
		_ = indexLock.Close()
		return fmt.Errorf("git index lock changed while reopening it")
	}
	return restoreIndexWithLock(indexLock, indexPath, snapshot)
}

func restoreIndexWithLock(indexLock *os.File, indexPath string, snapshot indexSnapshot) (retErr error) {
	lockPath := indexLock.Name()
	lockConsumed := false
	lockClosed := false
	defer func() {
		if lockConsumed {
			return
		}
		var closeErr error
		if !lockClosed {
			closeErr = indexLock.Close()
		}
		removeErr := os.Remove(lockPath)
		if os.IsNotExist(removeErr) {
			removeErr = nil
		}
		retErr = errors.Join(retErr, closeErr, removeErr)
	}()

	if !snapshot.exists {
		err := indexLock.Close()
		lockClosed = true
		if err != nil {
			return err
		}
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(lockPath); err != nil {
			return err
		}
		lockConsumed = true
		return nil
	}

	if err := indexLock.Truncate(0); err != nil {
		return err
	}
	if _, err := indexLock.Seek(0, 0); err != nil {
		return err
	}
	if err := indexLock.Chmod(snapshot.mode.Perm()); err != nil {
		return err
	}
	if _, err := indexLock.Write(snapshot.data); err != nil {
		return err
	}
	if err := indexLock.Sync(); err != nil {
		return err
	}
	err := indexLock.Close()
	lockClosed = true
	if err != nil {
		return err
	}
	if err := replaceFile(lockPath, indexPath); err != nil {
		return err
	}

	lockConsumed = true
	return nil
}

func intentToAddError(files []string) error {
	return fmt.Errorf(
		"intent-to-add entries are not supported safely: %q; fully stage or reset them and rerun",
		files,
	)
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

func wrapRepositoryLockError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to release repository locks: %w", err)
}

func wrapRestoreError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("failed to restore git index: %w", err)
}
