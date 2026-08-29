package commit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hasansino/commit/pkg/commit/models"
)

func TestNativePrivateIndexBornMissingIndexMatchesNativeGit(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	indexPath, err := gitOps.resolveGitPath(ctx, "index")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	oldHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	)))

	// A missing index on a born branch has the same semantics as an empty
	// index: tracked paths are staged deletions and same-path working files are
	// untracked. Preserve that native Git behavior rather than silently seeding
	// the private index from HEAD.
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	if !session.UsesExistingStaging() {
		t.Fatal("missing born index was not treated as an existing empty staged tree")
	}
	assertStagingIntegrationPaths(t, session.Files, []string{"tracked.txt"})
	result, err := gitOps.CreateCommit(ctx, session, "commit native empty-index state")
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash == "" {
		t.Fatal("created commit has no hash")
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "show", "-s", "--format=%P", "HEAD",
	))); got != oldHead {
		t.Fatalf("new commit parent = %q, want %q", got, oldHead)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "ls-tree", "--name-only", "HEAD",
	))); got != "" {
		t.Fatalf("missing-index commit tree = %q, want empty", got)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "status", "--porcelain=v1",
	))); got != "?? tracked.txt" {
		t.Fatalf("post-commit worktree status = %q, want untracked tracked.txt", got)
	}
}

func TestNativePrivateIndexRejectingHookCannotLeakStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "session change\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	headBefore := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	)))
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	privatePath := state.PrivateIndexPath
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	writeStagingIntegrationFile(t, repoPath, "hook-only.txt", "staged by rejecting hook\n")
	writeNativeAuditHook(
		t,
		repoPath,
		"pre-commit",
		"#!/bin/sh\ngit add -- hook-only.txt\nexit 37\n",
	)

	if result, err := gitOps.CreateCommit(ctx, session, "must be rejected"); err == nil {
		t.Fatalf("CreateCommit() = (%+v, nil), want rejecting-hook error", result)
	} else {
		var recovery *CommitRecoveryError
		if errors.As(err, &recovery) {
			t.Fatalf("rejecting hook unexpectedly required recovery: %v", err)
		}
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); got != headBefore {
		t.Fatalf("rejecting hook changed HEAD to %s, want %s", got, headBefore)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("rejecting hook changed the real index")
	}
	if _, err := os.Stat(state.IndexPath + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("rejecting hook left the real index locked: %v", err)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("FinishStaging did not remove rejected private index: %v", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "diff", "--cached", "--name-only",
	))); got != "" {
		t.Fatalf("rejecting hook leaked real staging: %q", got)
	}
}

func TestNativePrivateIndexCommitMessageHookRewriteIsReturned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "rewritten message change\n")
	writeNativeAuditHook(
		t,
		repoPath,
		"commit-msg",
		"#!/bin/sh\nprintf 'rewritten subject\\n\\nrewritten body\\n' > \"$1\"\n",
	)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })

	result, err := gitOps.CreateCommit(ctx, session, "selected message")
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the hook's terminal newline; readCommitMessage removes Git's
	// formatter newline, not content from the commit message itself.
	if want := "rewritten subject\n\nrewritten body\n"; result.Message != want {
		t.Fatalf("CreateCommit message = %q, want %q", result.Message, want)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "reflog", "-1", "--format=%gs",
	))); got != "commit: rewritten subject" {
		t.Fatalf("native reflog subject = %q", got)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
}

func TestNativePrivateIndexBlocksExternalWriterDuringCommit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "native commit change\n")
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	entered := filepath.Join(t.TempDir(), "hook-entered")
	release := filepath.Join(t.TempDir(), "hook-release")
	t.Setenv("NATIVE_AUDIT_HOOK_ENTERED", entered)
	t.Setenv("NATIVE_AUDIT_HOOK_RELEASE", release)
	writeNativeAuditHook(t, repoPath, "pre-commit", nativeAuditBarrierHook)

	type outcome struct {
		result models.CommitResult
		err    error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		result, err := gitOps.CreateCommit(ctx, session, "commit under real index lock")
		outcomes <- outcome{result: result, err: err}
	}()
	waitNativeAuditFile(t, entered)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	writeStagingIntegrationFile(t, repoPath, "external.txt", "external writer\n")
	cmd := exec.Command("git", "-C", repoPath, "add", "--", "external.txt")
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("external git add succeeded during native commit: %s", output)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-outcomes:
		if got.err != nil {
			t.Fatalf("CreateCommit() error = %v", got.err)
		}
		if got.result.Hash == "" {
			t.Fatal("CreateCommit() returned no hash")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CreateCommit did not finish after releasing hook")
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "show", "--format=", "--name-only", "HEAD",
	))); strings.Contains(got, "external.txt") {
		t.Fatalf("external writer entered commit: %q", got)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "diff", "--cached", "--name-only",
	))); got != "" {
		t.Fatalf("external writer changed final real index: %q", got)
	}
}

func TestNativePrivateIndexExternalHeadChangeDuringCommitRequiresRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "native commit change\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	entered := filepath.Join(t.TempDir(), "hook-entered")
	release := filepath.Join(t.TempDir(), "hook-release")
	t.Setenv("NATIVE_AUDIT_HOOK_ENTERED", entered)
	t.Setenv("NATIVE_AUDIT_HOOK_RELEASE", release)
	writeNativeAuditHook(t, repoPath, "pre-commit", nativeAuditBarrierHook)

	type outcome struct {
		result models.CommitResult
		err    error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		result, err := gitOps.CreateCommit(ctx, session, "racing native commit")
		outcomes <- outcome{result: result, err: err}
	}()
	waitNativeAuditFile(t, entered)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	oldHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	)))
	oldTree := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD^{tree}",
	)))
	externalHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t,
		repoPath,
		[]byte("external ref-only commit\n"),
		"commit-tree",
		oldTree,
		"-p",
		oldHead,
	)))
	headRef := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "symbolic-ref", "HEAD",
	)))
	runStagingIntegrationGit(t, repoPath, nil, "update-ref", headRef, externalHead, oldHead)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var got outcome
	select {
	case got = <-outcomes:
	case <-time.After(10 * time.Second):
		t.Fatal("CreateCommit did not finish after external HEAD race")
	}
	var recovery *CommitRecoveryError
	if !errors.As(got.err, &recovery) {
		t.Fatalf("CreateCommit() = (%+v, %v), want CommitRecoveryError", got.result, got.err)
	}
	if output, err := runNativeAuditGitError(
		repoPath,
		"merge-base", "--is-ancestor", externalHead, "HEAD",
	); err != nil {
		t.Fatalf("external HEAD was lost: %v: %s", err, output)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("external HEAD race changed the real index")
	}
	if _, err := os.Stat(state.IndexPath + ".lock"); err != nil {
		t.Fatalf("recovery did not retain real index lock: %v", err)
	}
	if err := gitOps.FinishStaging(session); !errors.As(err, &recovery) {
		t.Fatalf("FinishStaging() error = %v, want retained recovery", err)
	}
}

func TestNativePrivateIndexCancellationAfterHeadUpdateReconcilesSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "canceled commit change\n")
	session, err := gitOps.BeginStaging(context.Background(), nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	marker := filepath.Join(t.TempDir(), "post-commit-entered")
	survived := filepath.Join(t.TempDir(), "post-commit-survived")
	t.Setenv("NATIVE_AUDIT_HOOK_ENTERED", marker)
	t.Setenv("NATIVE_AUDIT_HOOK_SURVIVED", survived)
	writeNativeAuditHook(
		t,
		repoPath,
		"post-commit",
		`#!/bin/sh
: > "$NATIVE_AUDIT_HOOK_ENTERED"
sleep 1
printf 'late hook mutation\n' > late-hook.txt
git add -- late-hook.txt
git update-ref "$(git symbolic-ref HEAD)" HEAD^
: > "$NATIVE_AUDIT_HOOK_SURVIVED"
`,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result models.CommitResult
		err    error
	}
	outcomes := make(chan outcome, 1)
	go func() {
		result, err := gitOps.CreateCommit(ctx, session, "cancel after HEAD update")
		outcomes <- outcome{result: result, err: err}
	}()
	waitNativeAuditFile(t, marker)
	canceledAt := time.Now()
	cancel()

	var got outcome
	select {
	case got = <-outcomes:
	case <-time.After(8 * time.Second):
		t.Fatal("CreateCommit did not return after cancellation")
	}
	if elapsed := time.Since(canceledAt); elapsed >= time.Second {
		t.Fatalf("CreateCommit returned %v after cancellation, want prompt process-tree termination", elapsed)
	}
	var created *CommitCreatedError
	if !errors.As(got.err, &created) || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("CreateCommit() = (%+v, %v), want canceled CommitCreatedError", got.result, got.err)
	}
	if got.result.Hash == "" {
		t.Fatal("canceled created commit has no result hash")
	}
	if head := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); head != got.result.Hash {
		t.Fatalf("HEAD = %s, want reported created hash %s", head, got.result.Hash)
	}
	if _, err := os.Stat(state.IndexPath + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("safe canceled commit retained real index lock: %v", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "diff", "--cached", "--name-only",
	))); got != "" {
		t.Fatalf("safe canceled commit left staged changes: %q", got)
	}
	assertNativeAuditFileAbsentFor(t, survived, 1500*time.Millisecond)
	if _, err := os.Stat(filepath.Join(repoPath, "late-hook.txt")); !os.IsNotExist(err) {
		t.Fatalf("canceled hook survived to mutate the worktree: %v", err)
	}
	if head := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); head != got.result.Hash {
		t.Fatalf("canceled hook changed HEAD late to %s, want %s", head, got.result.Hash)
	}
}

func TestNativePrivateIndexRedirectedBackgroundHookCannotOutliveGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "background hook change\n")
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })

	survived := filepath.Join(t.TempDir(), "background-hook-survived")
	t.Setenv("NATIVE_AUDIT_HOOK_SURVIVED", survived)
	writeNativeAuditHook(
		t,
		repoPath,
		"post-commit",
		`#!/bin/sh
(
	sleep 0.5
	printf 'late background mutation\n' > late-background-hook.txt
	git add -- late-background-hook.txt
	git update-ref "$(git symbolic-ref HEAD)" HEAD^
	: > "$NATIVE_AUDIT_HOOK_SURVIVED"
) </dev/null >/dev/null 2>&1 &
`,
	)

	result, err := gitOps.CreateCommit(ctx, session, "contain background hook")
	if err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if result.Hash == "" {
		t.Fatal("CreateCommit() returned no hash")
	}
	assertNativeAuditFileAbsentFor(t, survived, time.Second)
	if _, err := os.Stat(filepath.Join(repoPath, "late-background-hook.txt")); !os.IsNotExist(err) {
		t.Fatalf("redirected background hook survived to mutate the worktree: %v", err)
	}
	if head := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "rev-parse", "HEAD",
	))); head != result.Hash {
		t.Fatalf("redirected background hook changed HEAD to %s, want %s", head, result.Hash)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "diff", "--cached", "--name-only",
	))); got != "" {
		t.Fatalf("redirected background hook left staged changes: %q", got)
	}
}

func TestNativePrivateIndexPrivateLockFailureRetainsRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "recovery commit change\n")
	indexBefore := stagingIntegrationIndexBytes(t, repoPath)
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	writeNativeAuditHook(
		t,
		repoPath,
		"post-commit",
		"#!/bin/sh\n: > \"$GIT_INDEX_FILE.lock\"\n",
	)

	result, err := gitOps.CreateCommit(ctx, session, "leave private index locked")
	var recovery *CommitRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("CreateCommit() = (%+v, %v), want CommitRecoveryError", result, err)
	}
	if got := stagingIntegrationIndexBytes(t, repoPath); !bytes.Equal(got, indexBefore) {
		t.Fatal("private-lock recovery changed the real index")
	}
	for _, path := range []string{
		state.IndexPath + ".lock",
		state.PrivateIndexPath,
		state.PrivateIndexPath + ".lock",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("recovery artifact %q was not retained: %v", path, err)
		}
	}
	if err := gitOps.FinishStaging(session); !errors.As(err, &recovery) {
		t.Fatalf("FinishStaging() error = %v, want CommitRecoveryError", err)
	}
	if state.LeaseHeld {
		t.Fatal("FinishStaging did not release in-process session lease")
	}
	for _, path := range []string{
		state.IndexPath + ".lock",
		state.PrivateIndexPath,
		state.PrivateIndexPath + ".lock",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("FinishStaging removed recovery artifact %q: %v", path, err)
		}
	}
}

const nativeAuditBarrierHook = `#!/bin/sh
: > "$NATIVE_AUDIT_HOOK_ENTERED"
while test ! -e "$NATIVE_AUDIT_HOOK_RELEASE"; do
	sleep 0.02
done
`

func writeNativeAuditHook(t *testing.T, repoPath, name, contents string) {
	t.Helper()
	path := filepath.Join(repoPath, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}

func waitNativeAuditFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertNativeAuditFileAbsentFor(t *testing.T, path string, duration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("canceled descendant survived and created %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func runNativeAuditGitError(repoPath string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	return cmd.CombinedOutput()
}
