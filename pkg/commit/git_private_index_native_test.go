package commit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativePrivateIndexCreatesUnbornCommit(t *testing.T) {
	repoPath, gitOps := newNativeSliceRepository(t)
	if err := os.WriteFile(filepath.Join(repoPath, "first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	indexPath, err := gitOps.resolveGitPath(ctx, "index")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(indexPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real index exists before staging: %v", err)
	}

	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	privatePath := state.PrivateIndexPath
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	if info, err := os.Stat(privatePath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("private index mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(indexPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("BeginStaging changed the real index: %v", err)
	}

	diff, err := gitOps.GetStagedDiff(ctx, session, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+first") {
		t.Fatalf("private staged diff = %q", diff)
	}
	result, err := gitOps.CreateCommit(ctx, session, "first commit")
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash == "" || result.Message != "first commit" {
		t.Fatalf("commit result = %#v", result)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(privatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private index was not removed: %v", err)
	}
	if got := nativeSliceGit(t, repoPath, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("real index retained committed entries: %q", got)
	}
}

func TestNativePrivateIndexPreservesPostCommitHookStaging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	repoPath, gitOps := newNativeSliceRepository(t)
	for name, contents := range map[string]string{"a.txt": "old a\n", "b.txt": "old b\n"} {
		if err := os.WriteFile(filepath.Join(repoPath, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nativeSliceGit(t, repoPath, "add", "--", "a.txt", "b.txt")
	nativeSliceGit(t, repoPath, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repoPath, "a.txt"), []byte("new a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "b.txt"), []byte("new b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(repoPath, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\ngit add -- b.txt\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	session, err := gitOps.BeginStaging(ctx, nil, []string{"a.txt"}, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	privatePath := state.PrivateIndexPath
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	result, err := gitOps.CreateCommit(ctx, session, "change a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "change a" {
		t.Fatalf("commit message = %q", result.Message)
	}
	if got := nativeSliceGit(t, repoPath, "show", "HEAD:b.txt"); got != "old b" {
		t.Fatalf("hook-staged file entered commit: %q", got)
	}
	if got := nativeSliceGit(t, repoPath, "diff", "--cached", "--name-only"); got != "b.txt" {
		t.Fatalf("real staged set after post-commit hook = %q, want b.txt", got)
	}
	if info, err := os.Stat(privatePath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("private index mode after commit = %o, want 600", info.Mode().Perm())
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
}

func newNativeSliceRepository(t *testing.T) (string, *gitOperations) {
	t.Helper()
	repoPath := t.TempDir()
	nativeSliceGit(t, repoPath, "init", "--quiet")
	nativeSliceGit(t, repoPath, "config", "user.name", "Native Test")
	nativeSliceGit(t, repoPath, "config", "user.email", "native@example.test")
	gitOps, err := newGitOperations(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	return repoPath, gitOps
}

func nativeSliceGit(t *testing.T, repoPath string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
