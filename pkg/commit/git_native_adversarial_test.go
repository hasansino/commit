package commit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeCommitPostCommitHeadReplacementRequiresRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook uses a POSIX shell")
	}
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "selected change\n")
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	writeNativeAuditHook(t, repoPath, "post-commit", `#!/bin/sh
set -eu
parent=$(git rev-parse HEAD^)
replacement=$(printf 'replacement sibling\n' | git commit-tree "$parent^{tree}" -p "$parent")
current=$(git rev-parse HEAD)
git update-ref "$(git symbolic-ref HEAD)" "$replacement" "$current"
`)

	_, err = gitOps.CreateCommit(ctx, session, "selected commit")
	var recovery *CommitRecoveryError
	if !errors.As(err, &recovery) {
		t.Fatalf("CreateCommit() error = %v, want CommitRecoveryError", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "show", "HEAD:tracked.txt",
	))); got != "base" {
		t.Fatalf("replacement HEAD contains %q, want original content", got)
	}
	if _, err := os.Stat(state.IndexPath + ".lock"); err != nil {
		t.Fatalf("recovery did not retain the real index lock: %v", err)
	}
	if err := gitOps.FinishStaging(session); !errors.As(err, &recovery) {
		t.Fatalf("FinishStaging() error = %v, want retained recovery", err)
	}
}

func TestNativeCommitForcesOneAuditReflogEntryWhenDisabledByConfig(t *testing.T) {
	ctx := context.Background()
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	runStagingIntegrationGit(t, repoPath, nil, "config", "core.logAllRefUpdates", "false")
	headLog, err := gitOps.resolveGitPath(ctx, "logs/HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(headLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	writeStagingIntegrationFile(t, repoPath, "tracked.txt", "reflogged change\n")
	session, err := gitOps.BeginStaging(ctx, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	result, err := gitOps.CreateCommit(ctx, session, "audited commit")
	if err != nil {
		t.Fatal(err)
	}
	if result.Hash == "" {
		t.Fatal("CreateCommit() returned no hash")
	}
	if _, err := os.Stat(headLog); err != nil {
		t.Fatalf("native commit did not create its required HEAD reflog: %v", err)
	}
	if got := strings.TrimSpace(string(runStagingIntegrationGit(
		t, repoPath, nil, "config", "--type=bool", "core.logAllRefUpdates",
	))); got != "false" {
		t.Fatalf("repository core.logAllRefUpdates = %q, want unchanged false", got)
	}
}

func TestNativePushPartialMultiURLFailureIsIndeterminate(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	firstRemote := newNativeBareRepo(t)
	missingRemote := filepath.Join(t.TempDir(), "missing.git")
	runNativeTestGit(t, repoPath, "remote", "add", "publish", firstRemote)
	runNativeTestGit(t, repoPath, "config", "--add", "remote.publish.pushurl", firstRemote)
	runNativeTestGit(t, repoPath, "config", "--add", "remote.publish.pushurl", missingRemote)
	runNativeTestGit(t, repoPath, "config", "remote.pushDefault", "publish")
	runNativeTestGit(t, repoPath, "config", "push.default", "current")

	_, err := gitOps.pushNative(context.Background())
	var outcome *IndeterminateRemoteOutcomeError
	if !errors.As(err, &outcome) {
		t.Fatalf("pushNative() error = %T %v, want IndeterminateRemoteOutcomeError", err, err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("ordinary partial push unexpectedly reports cancellation: %v", err)
	}
	remoteHead := strings.TrimSpace(string(runNativeTestGit(
		t, firstRemote, "rev-parse", "refs/heads/topic",
	)))
	localHead := strings.TrimSpace(string(runNativeTestGit(t, repoPath, "rev-parse", "HEAD")))
	if remoteHead != localHead {
		t.Fatalf("first push URL has %s, want partial update %s", remoteHead, localHead)
	}
}

func TestNativePushSuppressesFollowTagsAndRefusesMirror(t *testing.T) {
	t.Run("follow tags", func(t *testing.T) {
		repoPath, gitOps := newNativePushTestRepo(t)
		remotePath := newNativeBareRepo(t)
		configureNativePushRemote(t, repoPath, "origin", remotePath)
		runNativeTestGit(t, repoPath, "config", "push.default", "current")
		runNativeTestGit(t, repoPath, "config", "push.followTags", "true")
		runNativeTestGit(t, repoPath, "tag", "-a", "v7.7.7", "-m", "reachable tag")

		if _, err := gitOps.pushNative(context.Background()); err != nil {
			t.Fatal(err)
		}
		if nativeTestGitSucceeds(remotePath, "show-ref", "--verify", "--quiet", "refs/tags/v7.7.7") {
			t.Fatal("branch push also pushed an annotated tag through push.followTags")
		}
	})

	t.Run("mirror", func(t *testing.T) {
		repoPath, gitOps := newNativePushTestRepo(t)
		remotePath := newNativeBareRepo(t)
		configureNativePushRemote(t, repoPath, "origin", remotePath)
		runNativeTestGit(t, repoPath, "config", "push.default", "current")
		runNativeTestGit(t, repoPath, "config", "remote.origin.mirror", "true")

		_, err := gitOps.pushNative(context.Background())
		if err == nil || !strings.Contains(err.Error(), "configured as a mirror") {
			t.Fatalf("pushNative() error = %v, want mirror refusal", err)
		}
		var outcome *IndeterminateRemoteOutcomeError
		if errors.As(err, &outcome) {
			t.Fatalf("pre-dispatch mirror refusal is indeterminate: %v", err)
		}
	})
}

func TestNativePushAutoSetupRemoteRecordsUpstream(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	remotePath := newNativeBareRepo(t)
	configureNativePushRemote(t, repoPath, "origin", remotePath)
	runNativeTestGit(t, repoPath, "config", "push.autoSetupRemote", "true")

	if _, err := gitOps.pushNative(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(runNativeTestGit(
		t, repoPath, "config", "branch.topic.remote",
	))); got != "origin" {
		t.Fatalf("configured upstream remote = %q, want origin", got)
	}
	if got := strings.TrimSpace(string(runNativeTestGit(
		t, repoPath, "config", "branch.topic.merge",
	))); got != "refs/heads/topic" {
		t.Fatalf("configured upstream ref = %q, want refs/heads/topic", got)
	}
}

func TestNativeMergeRequestURLHonorsPushInsteadOf(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	runNativeTestGit(
		t,
		repoPath,
		"remote",
		"add",
		"publish",
		"https://github.example.com/team/project.git",
	)
	runNativeTestGit(
		t,
		repoPath,
		"config",
		"url.ssh://git@github.example.com/other/project.git.pushInsteadOf",
		"https://github.example.com/team/project.git",
	)

	got := gitOps.nativeMergeRequestURL(context.Background(), nativePushTarget{
		remote:    "publish",
		remoteRef: "refs/heads/topic",
	})
	if got != "" {
		t.Fatalf("nativeMergeRequestURL() = %q, want suppression for rewritten push repository", got)
	}
}

func TestGitCommandMayUseTerminalFindsSubcommandAfterGlobalOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "commit after config",
			args: []string{"-c", "core.logAllRefUpdates=true", "commit", "--file", "message"},
			want: true,
		},
		{
			name: "add after literal pathspecs",
			args: []string{"--literal-pathspecs", "add", "--pathspec-from-file=-"},
			want: true,
		},
		{
			name: "add after config and literal pathspecs",
			args: []string{
				"-c", "core.excludesFile=/tmp/empty", "--literal-pathspecs",
				"add", "--pathspec-from-file=-", "--pathspec-file-nul",
			},
			want: true,
		},
		{
			name: "noninteractive diff after literal pathspecs",
			args: []string{"--literal-pathspecs", "diff", "--cached"},
			want: false,
		},
		{
			name: "incomplete config option",
			args: []string{"-c"},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := gitCommandMayUseTerminal(nil, test.args); got != test.want {
				t.Fatalf("gitCommandMayUseTerminal(nil, %q) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}
