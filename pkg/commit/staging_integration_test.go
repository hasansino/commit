package commit

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestStagingIntegration_UnbornBranchCanCreateFirstCommit(t *testing.T) {
	repoPath := t.TempDir()
	runStagingIntegrationGit(t, repoPath, nil, "init", "-q")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "Integration Test")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.email", "integration@example.invalid")
	runStagingIntegrationGit(t, repoPath, nil, "config", "commit.gpgsign", "false")
	writeStagingIntegrationFile(t, repoPath, "first.txt", "first commit\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() on unborn branch error = %v", err)
	}
	assertStagingIntegrationPaths(t, session.files, []string{"first.txt"})

	wantBranch := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "symbolic-ref", "--short", "HEAD",
	)))
	if got, err := gitOps.GetCurrentBranch(); err != nil || got != wantBranch {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("GetCurrentBranch() = (%q, %v), want (%q, nil)", got, err, wantBranch)
	}
	if err := gitOps.CreateCommit(session, "first commit"); err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:first.txt")); got != "first commit\n" {
		t.Fatalf("HEAD:first.txt = %q, want first commit content", got)
	}
}

func TestStagingIntegration_ServiceCreatesFirstCommit(t *testing.T) {
	repoPath := t.TempDir()
	runStagingIntegrationGit(t, repoPath, nil, "init", "-q")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "Integration Test")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.email", "integration@example.invalid")
	runStagingIntegrationGit(t, repoPath, nil, "config", "commit.gpgsign", "false")
	writeStagingIntegrationFile(t, repoPath, "first.txt", "service first commit\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	service := &Service{
		logger: slog.New(slog.DiscardHandler),
		settings: &Settings{
			Auto:             true,
			MaxDiffSizeBytes: 1 << 20,
		},
		gitOps: gitOps,
		aiService: &simpleTestAdapter{
			hasProviders: true,
			commitMsg:    "first commit",
		},
	}

	if err := service.Execute(context.Background()); err != nil {
		t.Fatalf("Execute() on unborn branch error = %v", err)
	}
	got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:first.txt"))
	if got != "service first commit\n" {
		t.Fatalf("HEAD:first.txt = %q, want service commit content", got)
	}
}

func TestStagingIntegration_ExistingPartialStageIsCommittedExactly(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)

	const (
		basePartial     = "first: base\nsecond: base\n"
		stagedPartial   = "first: staged\nsecond: base\n"
		worktreePartial = "first: staged\nsecond: unstaged\n"
	)

	writeStagingIntegrationFile(t, repoPath, "partial.txt", basePartial)
	writeStagingIntegrationFile(t, repoPath, "other.txt", "other: base\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "partial.txt", "other.txt")
	runStagingIntegrationGit(t, repoPath, nil, "commit", "-q", "-m", "add fixture files")

	writeStagingIntegrationFile(t, repoPath, "partial.txt", worktreePartial)
	writeStagingIntegrationFile(t, repoPath, "other.txt", "other: unstaged\n")
	stagedOID := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, []byte(stagedPartial), "hash-object", "-w", "--stdin",
	)))
	runStagingIntegrationGit(
		t, repoPath, nil, "update-index", "--add", "--cacheinfo", "100644", stagedOID, "partial.txt",
	)

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(
		[]string{"partial.txt"},
		[]string{"other.txt"},
		false,
	)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if !session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = false, want true")
	}
	assertStagingIntegrationPaths(t, session.files, []string{"partial.txt"})
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("BeginStaging() rewrote an existing selectively staged index")
	}

	if err := gitOps.CreateCommit(session, "commit only the selected hunk"); err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(true) error = %v", err)
	}

	if got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:partial.txt")); got != stagedPartial {
		t.Fatalf("committed partial.txt = %q, want exact staged content %q", got, stagedPartial)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD:partial.txt",
	))); got != stagedOID {
		t.Fatalf("committed blob = %s, want staged blob %s", got, stagedOID)
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:other.txt")); got != "other: base\n" {
		t.Fatalf("unstaged other.txt was included in commit: got %q", got)
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
		t.Fatalf("post-commit cached diff is not empty: %q", got)
	}
	assertStagingIntegrationPaths(
		t,
		stagingIntegrationNULPaths(runStagingIntegrationGit(t, repoPath, nil, "diff", "--name-only", "-z")),
		[]string{"other.txt", "partial.txt"},
	)
	if got := readStagingIntegrationFile(t, repoPath, "partial.txt"); got != worktreePartial {
		t.Fatalf("working-tree partial.txt = %q, want unstaged content preserved %q", got, worktreePartial)
	}
}

func TestStagingIntegration_DryRunRollbackRestoresIndexBytes(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed but not committed\n")
	writeStagingIntegrationFile(t, repoPath, "untracked.txt", "new file\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.files, []string{"tracked.txt", "untracked.txt"})

	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("dry-run rollback did not restore the index byte-for-byte")
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
		t.Fatalf("dry-run left paths staged: %q", got)
	}
}

func TestStagingIntegration_NoChangesReturnsClosedSession(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if len(session.files) != 0 || !session.closed {
		t.Fatalf(
			"empty staging session = {files:%q closed:%v}, want no files and closed",
			session.files,
			session.closed,
		)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if session.leaseHeld {
		t.Fatal("FinishStaging() did not release the staging-session lease")
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("no-change preparation rewrote the index")
	}
}

func TestStagingIntegration_OverlappingSessionIsRejected(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "first session\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	first, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("first BeginStaging() error = %v", err)
	}
	if _, err := gitOps.BeginStaging(nil, nil, false); err == nil ||
		!strings.Contains(err.Error(), "already active") {
		_ = gitOps.FinishStaging(first)
		t.Fatalf("overlapping BeginStaging() error = %v, want active-session rejection", err)
	}
	if err := gitOps.FinishStaging(first); err != nil {
		t.Fatalf("first FinishStaging() error = %v", err)
	}

	second, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() after release error = %v", err)
	}
	if err := gitOps.FinishStaging(second); err != nil {
		t.Fatalf("second FinishStaging() error = %v", err)
	}
}

func TestStagingIntegration_FailedFinishIsTerminal(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "failed rollback\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	externalLock, err := acquireIndexLock(session.indexPath)
	if err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("acquireIndexLock() error = %v", err)
	}

	if err := gitOps.FinishStaging(session); err == nil {
		_ = discardIndexLock(externalLock)
		t.Fatal("FinishStaging() error = nil while index is locked")
	}
	if !session.closed || session.leaseHeld {
		_ = discardIndexLock(externalLock)
		t.Fatalf("failed finish did not consume session: closed=%v leaseHeld=%v", session.closed, session.leaseHeld)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		_ = discardIndexLock(externalLock)
		t.Fatalf("second FinishStaging() error = %v, want terminal no-op", err)
	}
	if err := discardIndexLock(externalLock); err != nil {
		t.Fatalf("discardIndexLock() error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); bytes.Equal(got, indexBefore) {
		t.Fatal("failed rollback unexpectedly reported failure after restoring the index")
	}

	next, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() after failed finish error = %v", err)
	}
	if err := gitOps.FinishStaging(next); err != nil {
		t.Fatalf("final FinishStaging() error = %v", err)
	}
}

func TestStagingIntegration_FinishErrorDoesNotPoisonService(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "service cleanup failure\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	indexPath, err := gitOps.resolveIndexPath()
	if err != nil {
		t.Fatalf("resolveIndexPath() error = %v", err)
	}

	var externalLock *os.File
	adapter := &simpleTestAdapter{
		hasProviders: true,
		commitMsg:    "dry run",
		beforeGenerate: func() error {
			if externalLock != nil {
				return nil
			}
			var lockErr error
			externalLock, lockErr = acquireIndexLock(indexPath)
			return lockErr
		},
	}
	service := &Service{
		logger: slog.New(slog.DiscardHandler),
		settings: &Settings{
			Auto:             true,
			DryRun:           true,
			MaxDiffSizeBytes: 1 << 20,
		},
		gitOps:    gitOps,
		aiService: adapter,
	}
	t.Cleanup(func() {
		_ = discardIndexLock(externalLock)
	})

	err = service.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "temporary staged changes may remain") {
		t.Fatalf("first Execute() error = %v, want incomplete-cleanup warning", err)
	}
	if err := discardIndexLock(externalLock); err != nil {
		t.Fatalf("discardIndexLock() error = %v", err)
	}
	externalLock = nil
	adapter.beforeGenerate = nil

	if err := service.Execute(context.Background()); err != nil {
		t.Fatalf("second Execute() after cleanup failure error = %v", err)
	}
}

func TestStagingIntegration_FailedCommitRollbackRestoresIndexBytes(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed before failed commit\n")
	// A repository-local empty value shadows any global identity and makes both
	// go-git and native Git reject commit creation deterministically.
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	if err := gitOps.CreateCommit(session, "this commit must fail"); err == nil {
		_ = gitOps.FinishStaging(session)
		t.Fatal("CreateCommit() error = nil, want failure")
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) error = %v", err)
	}

	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("failed-commit rollback did not restore the index byte-for-byte")
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:tracked.txt")); got != "base\n" {
		t.Fatalf("failed commit changed HEAD: tracked.txt = %q", got)
	}
}

func TestStagingIntegration_FinishTrueKeepsCommittedIndex(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "committed change\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.files, []string{"tracked.txt"})
	if err := gitOps.CreateCommit(session, "keep the committed index"); err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(true) error = %v", err)
	}

	indexAfter := stagingIntegrationIndexBytes(t, repoPath)
	if bytes.Equal(indexAfter, indexBefore) {
		t.Fatal("FinishStaging(true) restored the pre-commit index")
	}
	got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:tracked.txt"))
	if got != "committed change\n" {
		t.Fatalf("HEAD:tracked.txt = %q, want committed content", got)
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "status", "--porcelain=v1")); got != "" {
		t.Fatalf("repository is not clean after successful commit: %q", got)
	}
}

func TestStagingIntegration_IntentToAddWithStagedContentFailsClosed(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "actually staged\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "tracked.txt")
	stagedOID := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":tracked.txt",
	)))
	writeStagingIntegrationFile(t, repoPath, "intent.txt", "must not become an empty committed file\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "-N", "--", "intent.txt")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	if _, err := gitOps.BeginStaging(nil, nil, false); err == nil {
		t.Fatal("BeginStaging() error = nil with intent-to-add plus staged content")
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("intent-to-add rejection changed the real index")
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":tracked.txt",
	))); got != stagedOID {
		t.Fatalf("actual staged blob changed: got %s, want %s", got, stagedOID)
	}
}

func TestStagingIntegration_IntentToAddOnlyFailsClosed(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "intent.txt", "intent-to-add content\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "-N", "--", "intent.txt")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	if _, err := gitOps.BeginStaging(nil, nil, false); err == nil ||
		!strings.Contains(err.Error(), "intent-to-add") {
		t.Fatalf("BeginStaging() error = %v, want intent-to-add rejection", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("intent-to-add rejection changed the index")
	}
}

func TestStagingIntegration_LinkedWorktreeUsesItsOwnIndex(t *testing.T) {
	mainRepoPath := newStagingIntegrationRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runStagingIntegrationGit(
		t, mainRepoPath, nil, "worktree", "add", "-q", "-b", "c1-linked", linkedPath,
	)

	writeStagingIntegrationFile(t, mainRepoPath, "main-only.txt", "staged only in main worktree\n")
	runStagingIntegrationGit(t, mainRepoPath, nil, "add", "--", "main-only.txt")
	mainIndexBefore := stagingIntegrationIndexBytes(t, mainRepoPath)

	writeStagingIntegrationFile(t, linkedPath, "tracked.txt", "linked worktree commit\n")
	linkedIndexBefore := stagingIntegrationIndexBytes(t, linkedPath)
	gitOps := newStagingIntegrationGitOperations(t, linkedPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.usesExistingStaging() {
		t.Fatal("linked BeginStaging() saw the main worktree's staged index")
	}
	assertStagingIntegrationPaths(t, session.files, []string{"tracked.txt"})
	if got := stagingIntegrationIndexBytes(t, mainRepoPath); !bytes.Equal(got, mainIndexBefore) {
		_ = gitOps.FinishStaging(session)
		t.Fatal("linked-worktree staging changed the main worktree index")
	}
	if got := stagingIntegrationIndexBytes(t, linkedPath); bytes.Equal(got, linkedIndexBefore) {
		_ = gitOps.FinishStaging(session)
		t.Fatal("linked-worktree staging did not update its own index")
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) error = %v", err)
	}

	if got := stagingIntegrationIndexBytes(t, mainRepoPath); !bytes.Equal(got, mainIndexBefore) {
		t.Fatal("linked-worktree session changed the main worktree index")
	}
	if got := stagingIntegrationIndexBytes(t, linkedPath); !bytes.Equal(got, linkedIndexBefore) {
		t.Fatal("linked-worktree rollback did not restore its own index")
	}
	assertStagingIntegrationPaths(
		t,
		stagingIntegrationNULPaths(runStagingIntegrationGit(
			t, mainRepoPath, nil, "diff", "--cached", "--name-only", "-z",
		)),
		[]string{"main-only.txt"},
	)

	commitSession, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("second linked BeginStaging() error = %v", err)
	}
	if err := gitOps.CreateCommit(commitSession, "commit in linked worktree"); err != nil {
		_ = gitOps.FinishStaging(commitSession)
		t.Fatalf("linked CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(commitSession); err != nil {
		t.Fatalf("linked FinishStaging() error = %v", err)
	}
	got := string(runStagingIntegrationGit(t, linkedPath, nil, "show", "HEAD:tracked.txt"))
	if got != "linked worktree commit\n" {
		t.Fatalf("linked HEAD:tracked.txt = %q, want committed content", got)
	}
	if got := string(runStagingIntegrationGit(t, mainRepoPath, nil, "show", "HEAD:tracked.txt")); got != "base\n" {
		t.Fatalf("main HEAD changed through linked commit: tracked.txt = %q", got)
	}
	if got := stagingIntegrationIndexBytes(t, mainRepoPath); !bytes.Equal(got, mainIndexBefore) {
		t.Fatal("linked commit changed the main worktree index")
	}
	if got := readStagingIntegrationFile(t, linkedPath, "tracked.txt"); got != "linked worktree commit\n" {
		t.Fatalf("linked working tree was changed by rollback: tracked.txt = %q", got)
	}
}

func TestStagingIntegration_InvocationFromSubdirectoryUsesRepositoryRoot(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	subdirectory := filepath.Join(repoPath, "nested", "directory")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", subdirectory, err)
	}
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed at repository root\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, subdirectory)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.files, []string{"tracked.txt"})
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("subdirectory invocation did not restore the repository-root index")
	}
}

func TestStagingIntegration_PackedNestedBranchCanRollbackAndCommit(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	runStagingIntegrationGit(t, repoPath, nil, "checkout", "-q", "-b", "feature/nested")
	runStagingIntegrationGit(t, repoPath, nil, "pack-refs", "--all", "--prune")
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "change on packed nested branch\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	rollbackSession, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if err := gitOps.FinishStaging(rollbackSession); err != nil {
		t.Fatalf("FinishStaging(false) on packed nested branch error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("packed nested branch rollback did not restore the index")
	}

	commitSession, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("second BeginStaging() error = %v", err)
	}
	err = gitOps.CreateCommit(commitSession, "commit on packed nested branch")
	if err != nil {
		_ = gitOps.FinishStaging(commitSession)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(commitSession); err != nil {
		t.Fatalf("FinishStaging(true) error = %v", err)
	}
	got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:tracked.txt"))
	if got != "change on packed nested branch\n" {
		t.Fatalf("HEAD:tracked.txt = %q, want packed-branch commit", got)
	}
}

func TestStagingIntegration_ExistingStagedUnusualFilenameIsPreserved(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	filename := ":(exclude)odd\nname\t-leading-π.txt"
	writeStagingIntegrationFile(t, repoPath, filename, "unusual path\n")
	runStagingIntegrationGit(t, repoPath, nil, "--literal-pathspecs", "add", "--", filename)

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if !session.usesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = false, want true")
	}
	assertStagingIntegrationPaths(t, session.files, []string{filename})
	diff, err := gitOps.GetStagedDiff(1 << 20)
	if err != nil {
		t.Fatalf("GetStagedDiff() error = %v", err)
	}
	if !strings.Contains(diff, "unusual path") {
		t.Fatalf("GetStagedDiff() omitted the magic-looking filename: %q", diff)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("existing unusual-path staging changed after rollback")
	}
}

func TestStagingIntegration_MetadataOnlyIndexRewriteAllowsRollback(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculatively staged\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	preparedIndex := stagingIntegrationIndexBytes(t, repoPath)
	runStagingIntegrationGit(t, repoPath, nil, "update-index", "--index-version=4")
	if got := stagingIntegrationIndexBytes(t, repoPath); bytes.Equal(got, preparedIndex) {
		t.Fatal("test setup did not rewrite the index encoding")
	}

	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) rejected a metadata-only rewrite: %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("rollback after metadata refresh did not restore the original index")
	}
}

func TestStagingIntegration_ConcurrentIndexChangeIsNotOverwritten(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "staged by the session\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}

	writeStagingIntegrationFile(t, repoPath, "external.txt", "staged by another git process\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "external.txt")
	externalOID := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":external.txt",
	)))

	err = gitOps.FinishStaging(session)
	if err == nil || !strings.Contains(err.Error(), "git index changed") {
		t.Fatalf("FinishStaging(false) error = %v, want concurrent index change", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); bytes.Equal(got, indexBefore) {
		t.Fatal("rollback overwrote the concurrently changed index")
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":external.txt",
	))); got != externalOID {
		t.Fatalf("concurrent staged blob changed: got %s, want %s", got, externalOID)
	}
}

func TestStagingIntegration_ConcurrentHeadChangeIsNotOverwritten(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculatively staged\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}

	headRef := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "symbolic-ref", "HEAD",
	)))
	oldHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	)))
	tree := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD^{tree}",
	)))
	newHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t,
		repoPath,
		[]byte("external commit\n"),
		"commit-tree",
		tree,
		"-p",
		oldHead,
	)))
	runStagingIntegrationGit(t, repoPath, nil, "update-ref", headRef, newHead, oldHead)

	err = gitOps.CreateCommit(session, "must not overwrite external HEAD")
	if err == nil || !strings.Contains(err.Error(), "HEAD changed") {
		t.Fatalf("CreateCommit() error = %v, want concurrent HEAD error", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); got != newHead {
		t.Fatalf("CreateCommit() overwrote external HEAD: got %s, want %s", got, newHead)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging(false) after terminal HEAD conflict error = %v", err)
	}
}

func TestStagingIntegration_FilterPatternsMatchPathComponents(t *testing.T) {
	newFixture := func(t *testing.T) (string, *gitOperations) {
		t.Helper()
		repoPath := newStagingIntegrationRepo(t)
		files := map[string]string{
			"api":                       "exact include target\n",
			"dialog.go":                 "contains log\n",
			"log":                       "exact exclude target\n",
			"main.go":                   "contains go\n",
			"nested/build/output.txt":   "exact directory component\n",
			"nested/rebuild/output.txt": "directory substring only\n",
			"rapid.go":                  "contains api\n",
			"rebuilder.c":               "contains build\n",
		}
		for name, contents := range files {
			writeStagingIntegrationFile(t, repoPath, name, contents)
		}
		return repoPath, newStagingIntegrationGitOperations(t, repoPath)
	}

	t.Run("exclude literals and directories", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		session, err := gitOps.BeginStaging([]string{"log", "go", "build/"}, nil, false)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.files, []string{
			"api",
			"dialog.go",
			"main.go",
			"nested/rebuild/output.txt",
			"rapid.go",
			"rebuilder.c",
		})
		if err := gitOps.FinishStaging(session); err != nil {
			t.Fatalf("FinishStaging() error = %v", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("rollback left paths staged: %q", got)
		}
	})

	t.Run("include literal", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		session, err := gitOps.BeginStaging(nil, []string{"api"}, false)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.files, []string{"api"})
		if err := gitOps.FinishStaging(session); err != nil {
			t.Fatalf("FinishStaging() error = %v", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("rollback left paths staged: %q", got)
		}
	})

	t.Run("global literal", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		globalIgnorePath := filepath.Join(t.TempDir(), "global-ignore")
		if err := os.WriteFile(globalIgnorePath, []byte("build\n"), 0o644); err != nil {
			t.Fatalf("write global ignore: %v", err)
		}
		runStagingIntegrationGit(
			t,
			repoPath,
			nil,
			"config",
			"core.excludesFile",
			globalIgnorePath,
		)

		session, err := gitOps.BeginStaging(nil, nil, true)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.files, []string{
			"api",
			"dialog.go",
			"log",
			"main.go",
			"nested/rebuild/output.txt",
			"rapid.go",
			"rebuilder.c",
		})
		if err := gitOps.FinishStaging(session); err != nil {
			t.Fatalf("FinishStaging() error = %v", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("rollback left paths staged: %q", got)
		}
	})

	t.Run("selector negation is rejected", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		if _, err := gitOps.BeginStaging([]string{"!api"}, nil, false); err == nil ||
			!strings.Contains(err.Error(), "positive selectors") {
			t.Fatalf("BeginStaging() error = %v, want unsupported-negation error", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("rejected pattern left paths staged: %q", got)
		}
	})
}

func TestNewGitOperationsRejectsAlternateIndexEnvironment(t *testing.T) {
	repoPath := newStagingIntegrationRepo(t)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "alternate-index"))

	if _, err := newGitOperations(repoPath); err == nil || !strings.Contains(err.Error(), "GIT_INDEX_FILE") {
		t.Fatalf("newGitOperations() error = %v, want unsupported GIT_INDEX_FILE", err)
	}
}

func newStagingIntegrationRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	runStagingIntegrationGit(t, repoPath, nil, "init", "-q")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "Integration Test")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.email", "integration@example.invalid")
	runStagingIntegrationGit(t, repoPath, nil, "config", "commit.gpgsign", "false")
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "base\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "tracked.txt")
	runStagingIntegrationGit(t, repoPath, nil, "commit", "-q", "-m", "initial")
	return repoPath
}

func newStagingIntegrationGitOperations(t *testing.T, repoPath string) *gitOperations {
	t.Helper()
	gitOps, err := newGitOperations(repoPath)
	if err != nil {
		t.Fatalf("newGitOperations(%q) error = %v", repoPath, err)
	}
	return gitOps
}

func runStagingIntegrationGit(t *testing.T, repoPath string, stdin []byte, args ...string) []byte {
	t.Helper()
	commandArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C",
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	return output
}

func stagingIntegrationIndexBytes(t *testing.T, repoPath string) []byte {
	t.Helper()
	indexPath := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "--git-path", "index",
	)))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repoPath, indexPath)
	}
	contents, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index %q: %v", indexPath, err)
	}
	return contents
}

func writeStagingIntegrationFile(t *testing.T, repoPath, name, contents string) {
	t.Helper()
	path := filepath.Join(repoPath, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %q: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %q: %v", name, err)
	}
}

func readStagingIntegrationFile(t *testing.T, repoPath, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	return string(contents)
}

func stagingIntegrationNULPaths(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			paths = append(paths, string(part))
		}
	}
	return paths
}

func assertStagingIntegrationPaths(t *testing.T, got, want []string) {
	t.Helper()
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if fmt.Sprint(gotCopy) != fmt.Sprint(wantCopy) {
		t.Fatalf("paths = %q, want %q", gotCopy, wantCopy)
	}
}
