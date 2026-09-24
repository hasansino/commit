package commit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hasansino/commit/pkg/commit/models"
)

// CommitCreatedError reports that HEAD and the real index were updated even
// though native Git (or a post-commit verification step) returned an error.
type CommitCreatedError struct {
	Hash  string
	Cause error
}

func (e *CommitCreatedError) Error() string {
	return fmt.Sprintf("commit %s was created, but an error followed: %v", e.Hash, e.Cause)
}

func (e *CommitCreatedError) Unwrap() error {
	return e.Cause
}

type CommitRecoveryError = models.CommitRecoveryError

type nativeCommitLocks struct {
	realIndex *os.File
	private   *os.File
	head      []*os.File
	preserve  bool
}

func (l *nativeCommitLocks) releaseHead() error {
	locks := l.head
	l.head = nil
	return discardLocks(locks)
}

func (l *nativeCommitLocks) releaseAll() error {
	headErr := wrapHeadLockError(l.releaseHead())
	private := l.private
	l.private = nil
	privateErr := wrapIndexLockError(discardIndexLock(private))

	var realErr error
	if l.realIndex != nil {
		real := l.realIndex
		l.realIndex = nil
		if l.preserve {
			realErr = real.Close()
		} else {
			realErr = discardIndexLock(real)
		}
	}
	return errors.Join(headErr, privateErr, wrapIndexLockError(realErr))
}

func (l *nativeCommitLocks) discardRealIndex() error {
	real := l.realIndex
	l.realIndex = nil
	return discardIndexLock(real)
}

func (l *nativeCommitLocks) promoteRealIndex(
	indexPath string,
	snapshot indexSnapshot,
) error {
	if l.realIndex == nil {
		return fmt.Errorf("real git index lock is not held")
	}
	if err := writeIndexSnapshotToOpenLock(l.realIndex, snapshot); err != nil {
		return fmt.Errorf("failed to write final git index: %w", err)
	}
	lockPath := l.realIndex.Name()
	if err := l.realIndex.Close(); err != nil {
		l.realIndex = nil
		return fmt.Errorf("failed to close final git index: %w", err)
	}
	l.realIndex = nil
	if err := replaceFile(lockPath, indexPath); err != nil {
		return fmt.Errorf("failed to promote final git index: %w", err)
	}
	return nil
}

func (g *gitOperations) CreateCommit(
	ctx context.Context,
	session *models.StagingSessionState,
	message string,
) (result models.CommitResult, retErr error) {
	if err := g.validatePreparedStagingSession(session); err != nil {
		return models.CommitResult{}, err
	}
	state := session

	messagePath, err := createCommitMessageFile(state.IndexPath, message)
	if err != nil {
		return models.CommitResult{}, err
	}
	defer func() {
		if err := os.Remove(messagePath); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("failed to remove commit message file: %w", err))
		}
	}()
	locks := &nativeCommitLocks{}
	defer func() {
		retErr = errors.Join(retErr, locks.releaseAll())
	}()

	locks.realIndex, err = acquireIndexLock(state.IndexPath)
	if err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, err
	}
	locks.head, err = g.acquireHeadLocks(ctx, state.ExpectedHead)
	if err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, err
	}
	if err := g.verifyRepositoryInputs(
		ctx,
		state.IndexPath,
		state.PrivateIndexPath,
		state.BaseTree,
		state.ExpectedHead,
		state.IndexState,
		state.RawIndexState,
		state.PrivateIndexState,
	); err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, err
	}

	privateBeforeCommit, err := snapshotIndex(state.PrivateIndexPath)
	if err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, fmt.Errorf("failed to snapshot private git index: %w", err)
	}
	if !privateBeforeCommit.exists {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, fmt.Errorf("private git index disappeared before commit")
	}
	privateBeforeCommit.mode = state.IndexMode
	if err := writeIndexSnapshotToOpenLock(locks.realIndex, privateBeforeCommit); err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, fmt.Errorf("failed to seed git index recovery lock: %w", err)
	}
	headReflogBefore, err := g.nativeReflogSnapshot(ctx, "HEAD", 1)
	if err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, fmt.Errorf("failed to snapshot HEAD reflog: %w", err)
	}
	if err := locks.releaseHead(); err != nil {
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		return models.CommitResult{}, wrapHeadLockError(err)
	}

	// The real index lock remains continuously present while native Git commits
	// through the private index. Hooks and signing therefore see native behavior,
	// while ordinary external Git commands cannot consume speculative staging.
	locks.preserve = true
	state.Phase = models.StagingSessionCommitting
	_, commitErr := g.runGit(
		ctx,
		state.PrivateIndexPath,
		os.Stdin,
		"-c",
		"core.logAllRefUpdates=true",
		"commit",
		"--cleanup=verbatim",
		"--file",
		messagePath,
	)

	reconcileContext, cancelReconcile := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelReconcile()
	locks.private, err = acquirePathLock(state.PrivateIndexPath, "private git index")
	if err != nil {
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			models.HeadSnapshot{},
			"wait for any surviving hook, inspect HEAD, then promote or remove the retained index lock",
			errors.Join(commitErr, err),
		)
	}
	if err := os.Chmod(state.PrivateIndexPath, 0o600); err != nil {
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			models.HeadSnapshot{},
			"protect the retained private index, inspect HEAD, then reconcile the indexes",
			errors.Join(commitErr, err),
		)
	}
	locks.head, err = g.acquireHeadLocks(reconcileContext, state.ExpectedHead)
	if err != nil {
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			models.HeadSnapshot{},
			"inspect HEAD, then promote or remove the retained index lock",
			errors.Join(commitErr, err),
		)
	}
	currentHead, err := g.snapshotHead(reconcileContext)
	if err != nil {
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			models.HeadSnapshot{},
			"inspect HEAD, then promote or remove the retained index lock",
			errors.Join(commitErr, err),
		)
	}

	if currentHead == state.ExpectedHead {
		if err := locks.discardRealIndex(); err != nil {
			return models.CommitResult{}, g.requireCommitRecovery(
				state,
				locks,
				currentHead,
				"remove the retained index lock after confirming HEAD is unchanged",
				errors.Join(commitErr, err),
			)
		}
		state.Phase = models.StagingSessionFailed
		state.Closed = true
		if commitErr == nil {
			commitErr = fmt.Errorf("git commit returned success without updating HEAD")
		}
		return models.CommitResult{}, fmt.Errorf("failed to create commit: %w", commitErr)
	}

	headReflogAfter, identityErr := g.nativeReflogSnapshot(reconcileContext, "HEAD", 2)
	createdHash := ""
	if identityErr == nil {
		createdHash, identityErr = nativeSingleReflogAdvance(headReflogBefore, headReflogAfter)
	}
	if identityErr != nil || createdHash != currentHead.Hash {
		if identityErr == nil {
			identityErr = fmt.Errorf(
				"HEAD is %s, but the native commit created %s",
				currentHead.Hash,
				createdHash,
			)
		}
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			currentHead,
			"inspect the native commit reflog entry and retained indexes before reconciling HEAD",
			errors.Join(commitErr, identityErr),
		)
	}

	directChild, err := g.isDirectChild(reconcileContext, state.ExpectedHead, currentHead)
	if err != nil || !directChild || currentHead.Name != state.ExpectedHead.Name {
		if err == nil {
			err = fmt.Errorf("HEAD did not advance to a direct child of the expected commit")
		}
		return models.CommitResult{}, g.requireCommitRecovery(
			state,
			locks,
			currentHead,
			"inspect the retained private index and index lock before reconciling HEAD",
			errors.Join(commitErr, err),
		)
	}
	var messageErr error
	if commitErr == nil {
		result, messageErr = g.resultForCommit(reconcileContext, currentHead.Hash, message)
	}
	if _, err := g.indexFingerprint(
		reconcileContext,
		state.PrivateIndexPath,
		currentHead.Hash,
	); err != nil {
		return result, g.requireCommitRecovery(
			state,
			locks,
			currentHead,
			"inspect the new commit and retained indexes before choosing the final index",
			errors.Join(
				commitErr,
				messageErr,
				fmt.Errorf("private index is invalid after commit: %w", err),
			),
		)
	}

	if commitErr != nil {
		matches, matchErr := g.privateIndexMatchesCommit(
			reconcileContext,
			state.PrivateIndexPath,
			currentHead.Hash,
		)
		if matchErr != nil || !matches {
			if matchErr == nil {
				matchErr = fmt.Errorf("private index does not match the new commit tree")
			}
			return models.CommitResult{}, g.requireCommitRecovery(
				state,
				locks,
				currentHead,
				"inspect the new commit and retained indexes before choosing the final index",
				errors.Join(commitErr, matchErr),
			)
		}
		result, messageErr = g.resultForCommit(reconcileContext, currentHead.Hash, message)
	}

	privateAfterCommit, err := snapshotIndex(state.PrivateIndexPath)
	if err != nil || !privateAfterCommit.exists {
		if err == nil {
			err = fmt.Errorf("private git index disappeared after commit")
		}
		return result, g.requireCommitRecovery(
			state,
			locks,
			currentHead,
			"inspect the new commit and retained index lock before choosing the final index",
			errors.Join(commitErr, messageErr, err),
		)
	}
	privateAfterCommit.mode = state.IndexMode
	if err := locks.promoteRealIndex(state.IndexPath, privateAfterCommit); err != nil {
		return result, g.requireCommitRecovery(
			state,
			locks,
			currentHead,
			"promote the retained index lock after confirming it contains the desired final index",
			errors.Join(commitErr, messageErr, err),
		)
	}

	state.Phase = models.StagingSessionCommitted
	state.Closed = true
	if commitErr != nil || messageErr != nil {
		return result, &CommitCreatedError{
			Hash:  currentHead.Hash,
			Cause: errors.Join(commitErr, messageErr),
		}
	}
	return result, nil
}

func (g *gitOperations) nativeReflogSnapshot(
	ctx context.Context,
	ref string,
	maxCount int,
) ([]string, error) {
	if _, err := g.runGit(ctx, "", nil, "reflog", "exists", ref); err != nil {
		if gitCommandExitedWith(err, 1) {
			return nil, nil
		}
		return nil, err
	}
	newestOutput, err := g.runGit(
		ctx,
		"",
		nil,
		"--no-replace-objects",
		"reflog",
		"show",
		"-z",
		fmt.Sprintf("--max-count=%d", maxCount),
		"--format=%H",
		ref,
	)
	if err != nil {
		return nil, err
	}
	return splitNullTerminated(newestOutput), nil
}

func nativeSingleReflogAdvance(before, after []string) (string, error) {
	if len(before) == 0 && len(after) == 1 {
		return after[0], nil
	}
	if len(before) == 1 && len(after) == 2 && after[1] == before[0] {
		return after[0], nil
	}
	if len(after) == 0 {
		return "", fmt.Errorf("HEAD reflog has no native commit entry")
	}
	if len(before) > 1 || len(after) > 2 {
		return "", fmt.Errorf(
			"invalid bounded HEAD reflog snapshots: before=%d after=%d",
			len(before),
			len(after),
		)
	}
	return "", fmt.Errorf("HEAD reflog changed outside the expected native commit entry")
}

func (g *gitOperations) requireCommitRecovery(
	state *models.StagingSessionState,
	locks *nativeCommitLocks,
	currentHead models.HeadSnapshot,
	action string,
	cause error,
) error {
	locks.preserve = true
	recovery := &CommitRecoveryError{
		StandardIndexLockPath: state.IndexPath + ".lock",
		PrivateIndexPath:      state.PrivateIndexPath,
		ExpectedHEAD:          describeHead(state.ExpectedHead),
		CurrentHEAD:           describeHead(currentHead),
		Action:                action,
		Cause:                 cause,
	}
	state.Phase = models.StagingSessionRecoveryRequired
	state.Closed = true
	state.Recovery = recovery
	return recovery
}

func (g *gitOperations) isDirectChild(
	ctx context.Context,
	expected, current models.HeadSnapshot,
) (bool, error) {
	if current.Hash == "" {
		return false, nil
	}
	output, err := g.runGit(
		ctx,
		"",
		nil,
		"--no-replace-objects",
		"show",
		"-s",
		"--format=%P",
		current.Hash,
	)
	if err != nil {
		return false, fmt.Errorf("failed to inspect new commit parents: %w", err)
	}
	parents := strings.Fields(string(output))
	if expected.Hash == "" {
		return len(parents) == 0, nil
	}
	return len(parents) == 1 && parents[0] == expected.Hash, nil
}

func (g *gitOperations) privateIndexMatchesCommit(
	ctx context.Context,
	indexPath, commitHash string,
) (bool, error) {
	_, err := g.runGit(
		ctx,
		indexPath,
		nil,
		"--literal-pathspecs",
		"diff",
		"--cached",
		"--quiet",
		"--no-ext-diff",
		commitHash,
		"--",
	)
	if err == nil {
		return true, nil
	}
	if gitCommandExitedWith(err, 1) {
		return false, nil
	}
	return false, fmt.Errorf("failed to compare private index with new commit: %w", err)
}

func (g *gitOperations) readCommitMessage(ctx context.Context, hash string) (string, error) {
	output, err := g.runGit(ctx, "", nil, "show", "-s", "--format=%B", hash)
	if err != nil {
		return "", fmt.Errorf("failed to read created commit message: %w", err)
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

func (g *gitOperations) resultForCommit(
	ctx context.Context,
	hash, fallbackMessage string,
) (models.CommitResult, error) {
	message, err := g.readCommitMessage(ctx, hash)
	if err != nil {
		message = fallbackMessage
	}
	return models.CommitResult{Hash: hash, Message: message}, err
}

func createCommitMessageFile(indexPath, message string) (_ string, retErr error) {
	file, err := os.CreateTemp(filepath.Dir(indexPath), ".commit-message-*")
	if err != nil {
		return "", fmt.Errorf("failed to create commit message file: %w", err)
	}
	path := file.Name()
	defer func() {
		if err := file.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
		if retErr != nil {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				retErr = errors.Join(retErr, err)
			}
		}
	}()
	if _, err := file.WriteString(message); err != nil {
		return "", fmt.Errorf("failed to write commit message: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("failed to sync commit message: %w", err)
	}
	return path, nil
}

func describeHead(head models.HeadSnapshot) string {
	if head.Name == "" && head.Hash == "" {
		return "unknown"
	}
	if head.Hash == "" {
		return head.Name + " (unborn)"
	}
	return head.Name + " at " + head.Hash
}
