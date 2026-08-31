package commit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestStagingIntegration_UnbornBranchCanCreateFirstCommit(t *testing.T) {
	ctx := context.Background()
	repoPath := t.TempDir()
	runStagingIntegrationGit(t, repoPath, nil, "init", "-q")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "Integration Test")
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.email", "integration@example.invalid")
	runStagingIntegrationGit(t, repoPath, nil, "config", "commit.gpgsign", "false")
	writeStagingIntegrationFile(t, repoPath, "first.txt", "first commit\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() on unborn branch error = %v", err)
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"first.txt"})

	wantBranch := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "symbolic-ref", "--short", "HEAD",
	)))
	if got, err := gitOps.GetCurrentBranch(ctx); err != nil || got != wantBranch {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("GetCurrentBranch() = (%q, %v), want (%q, nil)", got, err, wantBranch)
	}
	result, err := gitOps.CreateCommit(ctx, session, "first commit")
	if err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if result.Hash == "" || result.Message != "first commit" {
		t.Fatalf("CreateCommit() result = %+v, want a hash and original message", result)
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
	ctx := context.Background()
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
		ctx,
		[]string{"partial.txt"},
		[]string{"other.txt"},
		false,
	)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if !session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = false, want true")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"partial.txt"})
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("BeginStaging() rewrote an existing selectively staged index")
	}

	if _, err := gitOps.CreateCommit(ctx, session, "commit only the selected hunk"); err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
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

func TestStagingIntegration_DryRunLeavesRealIndexUnchanged(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed but not committed\n")
	writeStagingIntegrationFile(t, repoPath, "untracked.txt", "new file\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt", "untracked.txt"})
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("BeginStaging() changed the real index")
	}

	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("FinishStaging() changed the real index")
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
		t.Fatalf("dry-run left paths staged: %q", got)
	}
}

func TestStagingIntegration_NoChangesReturnsClosedSession(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	state := session
	if len(session.Files) != 0 || !state.Closed {
		t.Fatalf(
			"empty staging session = {files:%q closed:%v}, want no files and closed",
			session.Files,
			state.Closed,
		)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if state.LeaseHeld {
		t.Fatal("FinishStaging() did not release the staging-session lease")
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("no-change preparation rewrote the index")
	}
}

func TestStagingIntegration_OverlappingSessionIsRejected(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "first session\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	first, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("first BeginStaging() error = %v", err)
	}
	if _, err := gitOps.BeginStaging(ctx, nil, nil, false); err == nil ||
		!strings.Contains(err.Error(), "already active") {
		_ = gitOps.FinishStaging(first)
		t.Fatalf("overlapping BeginStaging() error = %v, want active-session rejection", err)
	}
	if err := gitOps.FinishStaging(first); err != nil {
		t.Fatalf("first FinishStaging() error = %v", err)
	}

	second, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() after release error = %v", err)
	}
	if err := gitOps.FinishStaging(second); err != nil {
		t.Fatalf("second FinishStaging() error = %v", err)
	}
}

func TestStagingIntegration_FinishRemovesPrivateIndexAndReleasesSession(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "private session\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	state := session
	privateIndexPath := state.PrivateIndexPath
	if _, err := os.Stat(privateIndexPath); err != nil {
		t.Fatalf("private index does not exist while session is open: %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if !state.Closed || state.LeaseHeld {
		t.Fatalf(
			"finished session = {closed:%v leaseHeld:%v}, want closed and released",
			state.Closed, state.LeaseHeld,
		)
	}
	if _, err := os.Stat(privateIndexPath); !os.IsNotExist(err) {
		t.Fatalf("private index still exists after FinishStaging(): %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("second FinishStaging() error = %v, want terminal no-op", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("finishing a private session changed the real index")
	}

	next, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() after FinishStaging() error = %v", err)
	}
	if err := gitOps.FinishStaging(next); err != nil {
		t.Fatalf("final FinishStaging() error = %v", err)
	}
}

func TestStagingIntegration_PrivateSessionIsInvisibleToExternalCommit(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculative tracked change\n")
	writeStagingIntegrationFile(t, repoPath, "untracked.txt", "speculative untracked file\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	headBefore := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	)))

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	t.Cleanup(func() {
		_ = gitOps.FinishStaging(session)
	})
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt", "untracked.txt"})
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("BeginStaging() exposed speculative files through the real index")
	}
	if got := runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only"); len(got) != 0 {
		t.Fatalf("ordinary Git sees speculative staged files: %q", got)
	}
	output, commitErr := runStagingIntegrationGitError(t, repoPath, nil, "commit", "-m", "external commit")
	if commitErr == nil {
		t.Fatalf("ordinary git commit consumed speculative files:\n%s", output)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); got != headBefore {
		t.Fatalf("external commit changed HEAD to %s, want %s", got, headBefore)
	}
	diff, err := gitOps.GetStagedDiff(ctx, session, 1<<20)
	if err != nil || strings.TrimSpace(diff) == "" {
		t.Fatalf("private GetStagedDiff() = (%q, %v), want speculative diff", diff, err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
}

func TestStagingIntegration_FailedCommitLeavesRealIndexUnchanged(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed before failed commit\n")
	// A repository-local empty value shadows any global identity and makes
	// native Git reject commit creation deterministically.
	runStagingIntegrationGit(t, repoPath, nil, "config", "user.name", "")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	if _, err := gitOps.CreateCommit(ctx, session, "this commit must fail"); err == nil {
		_ = gitOps.FinishStaging(session)
		t.Fatal("CreateCommit() error = nil, want failure")
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}

	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("failed commit changed the real index")
	}
	if got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:tracked.txt")); got != "base\n" {
		t.Fatalf("failed commit changed HEAD: tracked.txt = %q", got)
	}
}

func TestStagingIntegration_SuccessfulCommitPromotesCommittedIndex(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "committed change\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt"})
	if _, err := gitOps.CreateCommit(ctx, session, "keep the committed index"); err != nil {
		_ = gitOps.FinishStaging(session)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}

	indexAfter := stagingIntegrationIndexBytes(t, repoPath)
	if bytes.Equal(indexAfter, indexBefore) {
		t.Fatal("successful commit did not promote its private index")
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
	ctx := context.Background()
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
	if _, err := gitOps.BeginStaging(ctx, nil, nil, false); err == nil {
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
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "intent.txt", "intent-to-add content\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "-N", "--", "intent.txt")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	if _, err := gitOps.BeginStaging(ctx, nil, nil, false); err == nil ||
		!strings.Contains(err.Error(), "intent-to-add") {
		t.Fatalf("BeginStaging() error = %v, want intent-to-add rejection", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("intent-to-add rejection changed the index")
	}
}

func TestStagingIntegration_LinkedWorktreeUsesItsOwnIndex(t *testing.T) {
	ctx := context.Background()
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
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.UsesExistingStaging() {
		t.Fatal("linked BeginStaging() saw the main worktree's staged index")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt"})
	if got := stagingIntegrationIndexBytes(t, mainRepoPath); !bytes.Equal(got, mainIndexBefore) {
		_ = gitOps.FinishStaging(session)
		t.Fatal("linked-worktree staging changed the main worktree index")
	}
	if got := stagingIntegrationIndexBytes(t, linkedPath); !bytes.Equal(got, linkedIndexBefore) {
		_ = gitOps.FinishStaging(session)
		t.Fatal("linked-worktree private staging changed its real index")
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}

	if got := stagingIntegrationIndexBytes(t, mainRepoPath); !bytes.Equal(got, mainIndexBefore) {
		t.Fatal("linked-worktree session changed the main worktree index")
	}
	if got := stagingIntegrationIndexBytes(t, linkedPath); !bytes.Equal(got, linkedIndexBefore) {
		t.Fatal("FinishStaging() changed the linked worktree's real index")
	}
	assertStagingIntegrationPaths(
		t,
		stagingIntegrationNULPaths(runStagingIntegrationGit(
			t, mainRepoPath, nil, "diff", "--cached", "--name-only", "-z",
		)),
		[]string{"main-only.txt"},
	)

	commitSession, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("second linked BeginStaging() error = %v", err)
	}
	if _, err := gitOps.CreateCommit(ctx, commitSession, "commit in linked worktree"); err != nil {
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
		t.Fatalf("linked working tree was changed by finalization: tracked.txt = %q", got)
	}
}

func TestStagingIntegration_LinkedWorktreeHonorsInfoExclude(t *testing.T) {
	ctx := context.Background()
	mainRepoPath := newStagingIntegrationRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	runStagingIntegrationGit(
		t, mainRepoPath, nil, "worktree", "add", "-q", "-b", "info-exclude-linked", linkedPath,
	)

	excludePath := strings.TrimSpace(string(runStagingIntegrationGit(
		t, linkedPath, nil, "rev-parse", "--git-path", "info/exclude",
	)))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(linkedPath, excludePath)
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		t.Fatalf("create info/exclude directory: %v", err)
	}
	if err := os.WriteFile(excludePath, []byte("local-secret.env\n"), 0o600); err != nil {
		t.Fatalf("write linked-worktree info/exclude: %v", err)
	}
	writeStagingIntegrationFile(t, linkedPath, "local-secret.env", "do not stage\n")
	writeStagingIntegrationFile(t, linkedPath, "visible.txt", "stage this\n")

	gitOps := newStagingIntegrationGitOperations(t, linkedPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	assertStagingIntegrationPaths(t, session.Files, []string{"visible.txt"})
}

func TestStagingIntegration_InvocationFromSubdirectoryUsesRepositoryRoot(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	subdirectory := filepath.Join(repoPath, "nested", "directory")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", subdirectory, err)
	}
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed at repository root\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, subdirectory)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = true, want false")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt"})
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("subdirectory invocation did not restore the repository-root index")
	}
}

func TestStagingIntegration_PackedNestedBranchCanRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	runStagingIntegrationGit(t, repoPath, nil, "checkout", "-q", "-b", "feature/nested")
	runStagingIntegrationGit(t, repoPath, nil, "pack-refs", "--all", "--prune")
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "change on packed nested branch\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	cleanupSession, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if err := gitOps.FinishStaging(cleanupSession); err != nil {
		t.Fatalf("FinishStaging() on packed nested branch error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("private session changed the packed nested branch index")
	}

	commitSession, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("second BeginStaging() error = %v", err)
	}
	_, err = gitOps.CreateCommit(ctx, commitSession, "commit on packed nested branch")
	if err != nil {
		_ = gitOps.FinishStaging(commitSession)
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if err := gitOps.FinishStaging(commitSession); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	got := string(runStagingIntegrationGit(t, repoPath, nil, "show", "HEAD:tracked.txt"))
	if got != "change on packed nested branch\n" {
		t.Fatalf("HEAD:tracked.txt = %q, want packed-branch commit", got)
	}
}

func TestStagingIntegration_ExistingStagedUnusualFilenameIsPreserved(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	filename := ":(exclude)odd\nname\t-leading-π.txt"
	writeStagingIntegrationFile(t, repoPath, filename, "unusual path\n")
	runStagingIntegrationGit(t, repoPath, nil, "--literal-pathspecs", "add", "--", filename)

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if !session.UsesExistingStaging() {
		t.Fatal("BeginStaging() usingExisting = false, want true")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{filename})
	diff, err := gitOps.GetStagedDiff(ctx, session, 1<<20)
	if err != nil {
		t.Fatalf("GetStagedDiff() error = %v", err)
	}
	if !strings.Contains(diff, "unusual path") {
		t.Fatalf("GetStagedDiff() omitted the magic-looking filename: %q", diff)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("existing unusual-path staging changed after private-session cleanup")
	}
}

func TestStagingIntegration_GetStagedDiffRunsZeroContextOnce(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed content\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "tracked.txt")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	t.Cleanup(func() {
		_ = gitOps.FinishStaging(session)
	})
	tracePath := filepath.Join(t.TempDir(), "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", tracePath)

	diff, err := gitOps.GetStagedDiff(ctx, session, 1)
	if err != nil {
		t.Fatalf("GetStagedDiff() error = %v", err)
	}
	if len(diff) != 1 {
		t.Fatalf("len(GetStagedDiff()) = %d, want 1", len(diff))
	}

	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read Git trace: %v", err)
	}

	var zeroContextDiffs int
	for _, line := range bytes.Split(trace, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode Git trace event %q: %v", line, err)
		}
		if event.Event == "start" &&
			slices.Contains(event.Argv, "diff") &&
			slices.Contains(event.Argv, "--cached") &&
			slices.Contains(event.Argv, "-U0") {
			zeroContextDiffs++
		}
	}
	if zeroContextDiffs != 1 {
		t.Errorf("zero-context staged diff command count = %d, want 1", zeroContextDiffs)
	}
}

func TestStagingIntegration_MetadataOnlyRealIndexRewriteIsPreserved(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculatively staged\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("BeginStaging() changed the real index before metadata refresh")
	}
	runStagingIntegrationGit(t, repoPath, nil, "update-index", "--index-version=4")
	rewrittenIndex := stagingIntegrationIndexBytes(t, repoPath)
	if bytes.Equal(rewrittenIndex, indexBefore) {
		t.Fatal("test setup did not rewrite the index encoding")
	}

	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() rejected a metadata-only real-index rewrite: %v", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, rewrittenIndex) {
		t.Fatal("FinishStaging() overwrote the external metadata-only index rewrite")
	}
}

func TestStagingIntegration_MetadataOnlyIndexRewriteRejectsCommit(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculatively staged\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })

	runStagingIntegrationGit(t, repoPath, nil, "update-index", "--index-version=4")
	rewrittenIndex := stagingIntegrationIndexBytes(t, repoPath)
	if _, err := gitOps.CreateCommit(ctx, session, "must not overwrite index metadata"); err == nil ||
		!strings.Contains(err.Error(), "git index changed") {
		t.Fatalf("CreateCommit() error = %v, want concurrent index change", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, rewrittenIndex) {
		t.Fatal("CreateCommit() overwrote the external metadata-only index rewrite")
	}
}

func TestStagingIntegration_ConcurrentSemanticIndexChangeRejectsCommitAndIsPreserved(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "staged by the session\n")

	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}

	writeStagingIntegrationFile(t, repoPath, "external.txt", "staged by another git process\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", "external.txt")
	externalOID := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":external.txt",
	)))

	if _, err = gitOps.CreateCommit(ctx, session, "must not consume external staging"); err == nil ||
		!strings.Contains(err.Error(), "git index changed") {
		t.Fatalf("CreateCommit() error = %v, want concurrent index change", err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); bytes.Equal(got, indexBefore) {
		t.Fatal("CreateCommit() overwrote the concurrently changed index")
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", ":external.txt",
	))); got != externalOID {
		t.Fatalf("concurrent staged blob changed: got %s, want %s", got, externalOID)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() after rejected commit error = %v", err)
	}
}

func TestStagingIntegration_ConcurrentHeadChangeIsNotOverwritten(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "speculatively staged\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
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

	_, err = gitOps.CreateCommit(ctx, session, "must not overwrite external HEAD")
	if err == nil || !strings.Contains(err.Error(), "HEAD changed") {
		t.Fatalf("CreateCommit() error = %v, want concurrent HEAD error", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); got != newHead {
		t.Fatalf("CreateCommit() overwrote external HEAD: got %s, want %s", got, newHead)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() after terminal HEAD conflict error = %v", err)
	}
}

func TestStagingIntegration_NativeCleanFilterAndEOLNormalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture clean filter uses the POSIX sed utility")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	runStagingIntegrationGit(t, repoPath, nil, "config", "filter.scrub.clean", "sed s/raw/clean/g")
	runStagingIntegrationGit(t, repoPath, nil, "config", "filter.scrub.required", "true")
	writeStagingIntegrationFile(
		t,
		repoPath,
		".gitattributes",
		"filtered.dat filter=scrub text eol=lf\n",
	)
	writeStagingIntegrationFile(t, repoPath, "filtered.dat", "raw value\r\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	assertStagingIntegrationPaths(t, session.Files, []string{".gitattributes", "filtered.dat"})

	state := session
	staged, err := gitOps.runGit(ctx, state.PrivateIndexPath, nil, "show", ":filtered.dat")
	if err != nil {
		t.Fatalf("read private-index blob: %v", err)
	}
	if got, want := string(staged), "clean value\n"; got != want {
		t.Fatalf("private-index filtered.dat = %q, want native filtered content %q", got, want)
	}

	if _, err := gitOps.CreateCommit(ctx, session, "apply native clean conversion"); err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if got, want := string(runStagingIntegrationGit(
		t, repoPath, nil, "show", "HEAD:filtered.dat",
	)), "clean value\n"; got != want {
		t.Fatalf("committed filtered.dat = %q, want %q", got, want)
	}
}

func TestStagingIntegration_NativeCommitRunsHooksAndWritesReflog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture hooks use POSIX shell scripts")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "committed through hooks\n")

	hooksPath := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "--git-path", "hooks",
	)))
	if !filepath.IsAbs(hooksPath) {
		hooksPath = filepath.Join(repoPath, hooksPath)
	}
	writeStagingIntegrationExecutable(
		t,
		filepath.Join(hooksPath, "pre-commit"),
		"#!/bin/sh\nprintf 'pre-commit\\n' >> hook-order.log\n",
	)
	writeStagingIntegrationExecutable(
		t,
		filepath.Join(hooksPath, "commit-msg"),
		"#!/bin/sh\nprintf 'commit-msg\\n' >> hook-order.log\nprintf 'rewritten by commit-msg\\n' > \"$1\"\n",
	)
	writeStagingIntegrationExecutable(
		t,
		filepath.Join(hooksPath, "post-commit"),
		"#!/bin/sh\nprintf 'post-commit\\n' >> hook-order.log\n",
	)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	result, err := gitOps.CreateCommit(ctx, session, "selected message")
	if err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if result.Hash == "" || result.Message != "rewritten by commit-msg\n" {
		t.Fatalf("CreateCommit() result = %+v, want rewritten message and commit hash", result)
	}
	if got, want := readStagingIntegrationFile(t, repoPath, "hook-order.log"),
		"pre-commit\ncommit-msg\npost-commit\n"; got != want {
		t.Fatalf("hook order = %q, want %q", got, want)
	}

	reflog := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "reflog", "-1", "--format=%H%x00%gs", "HEAD",
	)))
	parts := strings.SplitN(reflog, "\x00", 2)
	if len(parts) != 2 || parts[0] != result.Hash || parts[1] != "commit: rewritten by commit-msg" {
		t.Fatalf("HEAD reflog entry = %q, want %s and rewritten subject", reflog, result.Hash)
	}
}

func TestStagingIntegration_WhitespaceOnlyChangesProducePromptDiff(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "trailing whitespace", content: "base \n"},
		{name: "carriage return at EOL", content: "base\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repoPath := newStagingIntegrationRepo(t)
			writeStagingIntegrationFile(t, repoPath, "tracked.txt", tt.content)

			gitOps := newStagingIntegrationGitOperations(t, repoPath)
			session, err := gitOps.BeginStaging(ctx, nil, nil, false)
			if err != nil {
				t.Fatalf("BeginStaging() error = %v", err)
			}
			defer func() { _ = gitOps.FinishStaging(session) }()
			diff, err := gitOps.GetStagedDiff(ctx, session, 1<<20)
			if err != nil {
				t.Fatalf("GetStagedDiff() error = %v", err)
			}
			if strings.TrimSpace(diff) == "" {
				t.Fatal("GetStagedDiff() returned no prompt diff for a real staged change")
			}
		})
	}
}

func TestStagingIntegration_BornRepositoryWithoutIndexUsesNativeEmptyIndexSemantics(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "changed after index removal\n")
	writeStagingIntegrationFile(t, repoPath, "untracked.txt", "new after index removal\n")
	indexPath := stagingIntegrationIndexPath(t, repoPath)
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove real index: %v", err)
	}

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() with missing real index error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	if !session.UsesExistingStaging() {
		t.Fatal("missing real index was not treated as native existing staged deletions")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt"})
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("BeginStaging() recreated or changed the missing real index: %v", err)
	}
	if _, err := gitOps.CreateCommit(ctx, session, "commit with missing real index"); err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if got := string(runStagingIntegrationGit(
		t, repoPath, nil, "ls-tree", "-r", "--name-only", "HEAD",
	)); got != "" {
		t.Fatalf("HEAD tree = %q, want native empty-index deletion commit", got)
	}
	assertStagingIntegrationPaths(
		t,
		stagingIntegrationNULPaths(runStagingIntegrationGit(
			t, repoPath, nil, "ls-files", "--others", "--exclude-standard", "-z", "--",
		)),
		[]string{"tracked.txt", "untracked.txt"},
	)
}

func TestStagingIntegration_PrivateIndexModeIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "private index mode\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	state := session
	info, err := os.Stat(state.PrivateIndexPath)
	if err != nil {
		t.Fatalf("stat private index: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("private index mode = %o, want %o", got, want)
	}
}

func TestStagingIntegration_FilterPatternsMatchPathComponents(t *testing.T) {
	ctx := context.Background()
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
		session, err := gitOps.BeginStaging(ctx, []string{"log", "go", "build/"}, nil, false)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.Files, []string{
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
			t.Fatalf("private session leaked paths into the real index: %q", got)
		}
	})

	t.Run("include literal", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		session, err := gitOps.BeginStaging(ctx, nil, []string{"api"}, false)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.Files, []string{"api"})
		if err := gitOps.FinishStaging(session); err != nil {
			t.Fatalf("FinishStaging() error = %v", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("private session leaked paths into the real index: %q", got)
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

		session, err := gitOps.BeginStaging(ctx, nil, nil, true)
		if err != nil {
			t.Fatalf("BeginStaging() error = %v", err)
		}
		assertStagingIntegrationPaths(t, session.Files, []string{
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
			t.Fatalf("private session leaked paths into the real index: %q", got)
		}
	})

	t.Run("selector negation is rejected", func(t *testing.T) {
		repoPath, gitOps := newFixture(t)
		if _, err := gitOps.BeginStaging(ctx, []string{"!api"}, nil, false); err == nil ||
			!strings.Contains(err.Error(), "positive selectors") {
			t.Fatalf("BeginStaging() error = %v, want unsupported-negation error", err)
		}
		if got := string(runStagingIntegrationGit(t, repoPath, nil, "diff", "--cached", "--name-only")); got != "" {
			t.Fatalf("rejected pattern left paths staged: %q", got)
		}
	})
}

func TestStagingIntegration_GlobalIgnoreNegation(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	files := map[string]string{
		"error.log":          "ignored root log\n",
		"generated/drop.txt": "ignored generated file\n",
		"generated/keep.txt": "reincluded generated file\n",
		"keep.log":           "reincluded root log\n",
		"main.go":            "ordinary file\n",
		"nested/error.log":   "ignored nested log\n",
		"nested/keep.log":    "reincluded nested log\n",
	}
	for name, contents := range files {
		writeStagingIntegrationFile(t, repoPath, name, contents)
	}

	globalIgnorePath := filepath.Join(t.TempDir(), "global-ignore")
	globalIgnore := "*.log\n!keep.log\ngenerated/**\n!generated/keep.txt\n"
	if err := os.WriteFile(globalIgnorePath, []byte(globalIgnore), 0o600); err != nil {
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

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, true)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	assertStagingIntegrationPaths(t, session.Files, []string{
		"generated/keep.txt",
		"keep.log",
		"main.go",
		"nested/keep.log",
	})
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatalf("FinishStaging() error = %v", err)
	}
	if got := string(runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"diff",
		"--cached",
		"--name-only",
	)); got != "" {
		t.Fatalf("private session leaked paths into the real index: %q", got)
	}
}

func TestStagingIntegration_DefaultXDGIgnoreAndTrackedIgnoredFile(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	xdgPath := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgPath)
	ignoreDirectory := filepath.Join(xdgPath, "git")
	if err := os.MkdirAll(ignoreDirectory, 0o755); err != nil {
		t.Fatalf("create XDG Git directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(ignoreDirectory, "ignore"),
		[]byte("tracked.txt\nlocal-secret.env\n"),
		0o600,
	); err != nil {
		t.Fatalf("write XDG global ignore: %v", err)
	}

	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "tracked changes remain eligible\n")
	writeStagingIntegrationFile(t, repoPath, "local-secret.env", "globally ignored\n")
	writeStagingIntegrationFile(t, repoPath, "visible.txt", "not ignored\n")

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, true)
	if err != nil {
		t.Fatalf("BeginStaging() error = %v", err)
	}
	defer func() { _ = gitOps.FinishStaging(session) }()
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt", "visible.txt"})
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
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	gitOps, err := newGitOperations(repoPath)
	if err != nil {
		t.Fatalf("newGitOperations(%q) error = %v", repoPath, err)
	}
	return gitOps
}

func runStagingIntegrationGit(t *testing.T, repoPath string, stdin []byte, args ...string) []byte {
	t.Helper()
	output, err := runStagingIntegrationGitError(t, repoPath, stdin, args...)
	if err != nil {
		commandArgs := append([]string{"-C", repoPath}, args...)
		t.Fatalf("git %s failed: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	return output
}

func runStagingIntegrationGitError(
	t *testing.T,
	repoPath string,
	stdin []byte,
	args ...string,
) ([]byte, error) {
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
	return cmd.CombinedOutput()
}

func stagingIntegrationIndexBytes(t *testing.T, repoPath string) []byte {
	t.Helper()
	indexPath := stagingIntegrationIndexPath(t, repoPath)
	contents, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index %q: %v", indexPath, err)
	}
	return contents
}

func stagingIntegrationIndexPath(t *testing.T, repoPath string) string {
	t.Helper()
	indexPath := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "--git-path", "index",
	)))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(repoPath, indexPath)
	}
	return filepath.Clean(indexPath)
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

func writeStagingIntegrationExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create executable parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %q: %v", path, err)
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
