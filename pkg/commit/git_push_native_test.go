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
	"time"
)

func TestResolveNativePushTarget_DefaultModes(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*testing.T, string)
		wantRemote  string
		wantRef     string
		wantSetup   bool
		wantErrText string
	}{
		{
			name: "current selects same-named branch without upstream",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "push.default", "current")
			},
			wantRemote: "origin",
			wantRef:    "refs/heads/topic",
		},
		{
			name: "upstream preserves differently named destination",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				configureNativeUpstream(t, repoPath, "origin", "refs/heads/review/topic")
				runNativeTestGit(t, repoPath, "config", "push.default", "upstream")
			},
			wantRemote: "origin",
			wantRef:    "refs/heads/review/topic",
		},
		{
			name: "tracking is upstream alias",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				configureNativeUpstream(t, repoPath, "origin", "refs/heads/review/topic")
				runNativeTestGit(t, repoPath, "config", "push.default", "tracking")
			},
			wantRemote: "origin",
			wantRef:    "refs/heads/review/topic",
		},
		{
			name: "simple accepts same named upstream",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				configureNativeUpstream(t, repoPath, "origin", "refs/heads/topic")
			},
			wantRemote: "origin",
			wantRef:    "refs/heads/topic",
		},
		{
			name: "simple refuses differently named upstream",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				configureNativeUpstream(t, repoPath, "origin", "refs/heads/review/topic")
			},
			wantErrText: "push.default=simple refuses",
		},
		{
			name: "simple refuses missing upstream in central workflow",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
			},
			wantErrText: "has no upstream branch",
		},
		{
			name: "simple acts as current in triangular workflow",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				configureNativePushRemote(t, repoPath, "publish", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "branch.topic.remote", "origin")
				runNativeTestGit(t, repoPath, "config", "remote.pushDefault", "publish")
			},
			wantRemote: "publish",
			wantRef:    "refs/heads/topic",
		},
		{
			name: "simple infers triangular workflow without configured pull remote",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "publish", newNativeBareRepo(t))
				configureNativePushRemote(t, repoPath, "backup", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "branch.topic.pushRemote", "publish")
			},
			wantRemote: "publish",
			wantRef:    "refs/heads/topic",
		},
		{
			name: "simple auto-setup selects and records current branch",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "push.autoSetupRemote", "true")
			},
			wantRemote: "origin",
			wantRef:    "refs/heads/topic",
			wantSetup:  true,
		},
		{
			name: "nothing refuses",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "push.default", "nothing")
			},
			wantErrText: "push.default=nothing",
		},
		{
			name: "matching refuses",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "push.default", "matching")
			},
			wantErrText: "push.default=matching",
		},
		{
			name: "malformed case is not normalized",
			configure: func(t *testing.T, repoPath string) {
				configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
				runNativeTestGit(t, repoPath, "config", "push.default", "CURRENT")
			},
			wantErrText: "malformed value for push.default",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			test.configure(t, repoPath)

			target, err := gitOps.resolveNativePushTarget(context.Background())
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("resolveNativePushTarget() error = %v, want containing %q", err, test.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNativePushTarget() error = %v", err)
			}
			if target.remote != test.wantRemote ||
				target.remoteRef != test.wantRef ||
				target.setUpstream != test.wantSetup {
				t.Fatalf(
					"resolveNativePushTarget() = remote %q ref %q setup %v, want remote %q ref %q setup %v",
					target.remote,
					target.remoteRef,
					target.setUpstream,
					test.wantRemote,
					test.wantRef,
					test.wantSetup,
				)
			}
		})
	}
}

func TestResolveNativePushTarget_ExplicitRefspecs(t *testing.T) {
	tests := []struct {
		name        string
		refspecs    []string
		wantRef     string
		wantForce   bool
		wantErrText string
	}{
		{
			name: "full destination", refspecs: []string{"HEAD:refs/heads/review/topic"},
			wantRef: "refs/heads/review/topic",
		},
		{
			name: "short destination", refspecs: []string{"topic:published"},
			wantRef: "refs/heads/published",
		},
		{
			name: "omitted destination", refspecs: []string{"refs/heads/topic"},
			wantRef: "refs/heads/topic",
		},
		{
			name: "forced exact mapping", refspecs: []string{"+HEAD:refs/heads/forced"},
			wantRef: "refs/heads/forced", wantForce: true,
		},
		{name: "matching", refspecs: []string{":"}, wantErrText: "ambiguous push"},
		{
			name: "wildcard", refspecs: []string{"refs/heads/*:refs/heads/*"},
			wantErrText: "ambiguous push",
		},
		{
			name: "negative", refspecs: []string{"^refs/heads/other"},
			wantErrText: "ambiguous push",
		},
		{
			name: "multiple", refspecs: []string{"HEAD:refs/heads/one", "HEAD:refs/heads/two"},
			wantErrText: "potentially multi-ref",
		},
		{
			name: "different source", refspecs: []string{"refs/heads/other:refs/heads/topic"},
			wantErrText: "does not select the current branch",
		},
		{
			name: "deletion", refspecs: []string{":refs/heads/topic"},
			wantErrText: "deletes a ref",
		},
		{
			name: "tag destination", refspecs: []string{"HEAD:refs/tags/topic"},
			wantErrText: "not a branch ref",
		},
		{
			name: "ambiguous HEAD destination", refspecs: []string{"HEAD:HEAD"},
			wantErrText: "ambiguous HEAD destination",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
			runNativeTestGit(t, repoPath, "config", "push.default", "nothing")
			for _, refspec := range test.refspecs {
				runNativeTestGit(t, repoPath, "config", "--add", "remote.origin.push", refspec)
			}

			target, err := gitOps.resolveNativePushTarget(context.Background())
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("resolveNativePushTarget() error = %v, want containing %q", err, test.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNativePushTarget() error = %v", err)
			}
			if target.remoteRef != test.wantRef || target.force != test.wantForce {
				t.Fatalf(
					"resolveNativePushTarget() = ref %q force %v, want ref %q force %v",
					target.remoteRef,
					target.force,
					test.wantRef,
					test.wantForce,
				)
			}
		})
	}
}

func TestResolveNativePushTarget_RejectsAmbiguousShortSource(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantErrText string
	}{
		{
			name:        "short source collides with tag",
			source:      "topic",
			wantErrText: "does not resolve uniquely",
		},
		{
			name:   "fully qualified branch remains unambiguous",
			source: "refs/heads/topic",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
			runNativeTestGit(t, repoPath, "tag", "topic")
			runNativeTestGit(
				t,
				repoPath,
				"config",
				"remote.origin.push",
				test.source+":published",
			)

			target, err := gitOps.resolveNativePushTarget(context.Background())
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf(
						"resolveNativePushTarget() error = %v, want containing %q",
						err,
						test.wantErrText,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveNativePushTarget() error = %v", err)
			}
			if target.remoteRef != "refs/heads/published" {
				t.Fatalf(
					"resolveNativePushTarget() destination = %q, want refs/heads/published",
					target.remoteRef,
				)
			}
		})
	}
}

func TestResolveNativePushTarget_RejectsOptionLikeShortSource(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
	runNativeTestGit(t, repoPath, "update-ref", "refs/heads/-topic", "HEAD")
	runNativeTestGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/-topic")
	runNativeTestGit(
		t,
		repoPath,
		"config",
		"remote.origin.push",
		"-topic:published",
	)

	_, err := gitOps.resolveNativePushTarget(context.Background())
	if err == nil || !strings.Contains(err.Error(), "use the fully qualified source") {
		t.Fatalf(
			"resolveNativePushTarget() error = %v, want fully qualified source guidance",
			err,
		)
	}
}

func TestResolveNativePushRemote_PrecedenceAndAmbiguity(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	for _, remote := range []string{"pull", "default-push", "branch-push"} {
		configureNativePushRemote(t, repoPath, remote, newNativeBareRepo(t))
	}
	runNativeTestGit(t, repoPath, "config", "branch.topic.remote", "pull")
	runNativeTestGit(t, repoPath, "config", "remote.pushDefault", "default-push")
	runNativeTestGit(t, repoPath, "config", "branch.topic.pushRemote", "branch-push")

	assertNativePushRemote(t, gitOps, "branch-push")
	runNativeTestGit(t, repoPath, "config", "--unset", "branch.topic.pushRemote")
	assertNativePushRemote(t, gitOps, "default-push")
	runNativeTestGit(t, repoPath, "config", "--unset", "remote.pushDefault")
	assertNativePushRemote(t, gitOps, "pull")
	runNativeTestGit(t, repoPath, "config", "--unset", "branch.topic.remote")

	if _, err := gitOps.resolveNativePushRemote(context.Background(), "topic"); err == nil ||
		!strings.Contains(err.Error(), "multiple remotes") {
		t.Fatalf("resolveNativePushRemote(ambiguous) error = %v", err)
	}
}

func TestPushNative_PushesExactDestinationAndUsesItInReviewURL(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	remotePath := newNativeBareRepo(t)
	configureNativePushRemoteWithURL(
		t,
		repoPath,
		"publish",
		"https://github.example.com/team/project.git",
		remotePath,
	)
	runNativeTestGit(t, repoPath, "config", "remote.pushDefault", "publish")
	runNativeTestGit(t, repoPath, "config", "push.default", "nothing")
	runNativeTestGit(t, repoPath, "config", "remote.publish.push", "HEAD:refs/heads/review/topic")
	runNativeTestGit(t, repoPath, "update-ref", "refs/remotes/publish/main", "HEAD")
	runNativeTestGit(
		t,
		repoPath,
		"symbolic-ref",
		"refs/remotes/publish/HEAD",
		"refs/remotes/publish/main",
	)

	gotURL, err := gitOps.pushNative(context.Background())
	if err != nil {
		t.Fatalf("pushNative() error = %v", err)
	}
	wantURL := "https://github.example.com/team/project/compare/main...review%2Ftopic?expand=1"
	if gotURL != wantURL {
		t.Fatalf("pushNative() URL = %q, want %q", gotURL, wantURL)
	}

	localHead := strings.TrimSpace(string(runNativeTestGit(t, repoPath, "rev-parse", "HEAD")))
	remoteHead := strings.TrimSpace(string(runNativeTestGit(
		t,
		remotePath,
		"rev-parse",
		"refs/heads/review/topic",
	)))
	if remoteHead != localHead {
		t.Fatalf("remote destination = %s, want %s", remoteHead, localHead)
	}
	if nativeTestGitSucceeds(remotePath, "show-ref", "--verify", "--quiet", "refs/heads/topic") {
		t.Fatal("pushNative() also created the local branch name on the remote")
	}
}

func TestPushNative_RefusesDetachedHeadAndHonorsCancellation(t *testing.T) {
	t.Run("detached HEAD", func(t *testing.T) {
		repoPath, gitOps := newNativePushTestRepo(t)
		configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
		runNativeTestGit(t, repoPath, "checkout", "--detach", "-q")

		_, err := gitOps.pushNative(context.Background())
		if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
			t.Fatalf("pushNative() error = %v, want detached HEAD refusal", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		repoPath, gitOps := newNativePushTestRepo(t)
		configureNativePushRemote(t, repoPath, "origin", newNativeBareRepo(t))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := gitOps.pushNative(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pushNative() error = %v, want context.Canceled", err)
		}
	})
}

func TestNativeRemoteDispatchError_IsTypedAfterDispatch(t *testing.T) {
	cause := &gitCommandError{
		args:       []string{"push"},
		cause:      context.DeadlineExceeded,
		dispatched: true,
	}
	err := nativeRemoteDispatchError(cause, "branch push")
	var outcome *IndeterminateRemoteOutcomeError
	if !errors.As(err, &outcome) {
		t.Fatalf("nativeRemoteDispatchError() = %T %v, want IndeterminateRemoteOutcomeError", err, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("nativeRemoteDispatchError() = %v, want wrapped context deadline", err)
	}
	if !strings.Contains(err.Error(), "remote may have updated") {
		t.Fatalf("nativeRemoteDispatchError() = %q, want indeterminate-outcome wording", err)
	}
	if outcome.Operation != "branch push" {
		t.Fatalf("IndeterminateRemoteOutcomeError.Operation = %q", outcome.Operation)
	}
	undispatched := &gitCommandError{args: []string{"push"}, cause: context.Canceled}
	if got := nativeRemoteDispatchError(undispatched, "branch push"); got != nil {
		t.Fatalf("nativeRemoteDispatchError(undispatched) = %v, want nil", got)
	}
}

func TestNativePushes_ReportIndeterminateCancellationAfterDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX receive hook")
	}

	tests := []struct {
		name      string
		operation string
		invoke    func(context.Context, *gitOperations) error
	}{
		{
			name:      "branch",
			operation: "branch push",
			invoke: func(ctx context.Context, gitOps *gitOperations) error {
				_, err := gitOps.pushNative(ctx)
				return err
			},
		},
		{
			name:      "tag",
			operation: "tag push",
			invoke: func(ctx context.Context, gitOps *gitOperations) error {
				return gitOps.pushTagNative(ctx, "v9.9.9")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			remotePath := newNativeBareRepo(t)
			configureNativePushRemote(t, repoPath, "origin", remotePath)
			runNativeTestGit(t, repoPath, "config", "push.default", "current")
			if test.name == "tag" {
				runNativeTestGit(t, repoPath, "tag", "v9.9.9")
			}

			marker := filepath.Join(t.TempDir(), "hook-entered")
			t.Setenv("NATIVE_PUSH_HOOK_MARKER", marker)
			hook := []byte("#!/bin/sh\ntouch \"$NATIVE_PUSH_HOOK_MARKER\"\nsleep 1\n")
			if err := os.WriteFile(filepath.Join(remotePath, "hooks", "pre-receive"), hook, 0o700); err != nil {
				t.Fatalf("write pre-receive hook: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				result <- test.invoke(ctx, gitOps)
			}()

			if !waitForNativePushMarker(marker, 5*time.Second) {
				cancel()
				<-result
				t.Fatal("push did not reach the remote receive hook")
			}
			cancel()
			err := <-result

			var outcome *IndeterminateRemoteOutcomeError
			if !errors.As(err, &outcome) {
				t.Fatalf("push error = %T %v, want IndeterminateRemoteOutcomeError", err, err)
			}
			if outcome.Operation != test.operation {
				t.Fatalf("outcome operation = %q, want %q", outcome.Operation, test.operation)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("push error = %v, want wrapped context.Canceled", err)
			}
		})
	}
}

func TestNativeMergeRequestURL_SuppressesDifferentHostedPushRepository(t *testing.T) {
	tests := []struct {
		name    string
		pushURL string
		wantURL string
	}{
		{
			name:    "different hosted repository",
			pushURL: "ssh://git@github.example.com/other/project.git",
		},
		{
			name:    "same hosted repository over SSH",
			pushURL: "ssh://deploy@github.example.com/team/project.git",
			wantURL: "https://github.example.com/team/project/compare/main...review%2Ftopic?expand=1",
		},
		{
			name:    "local push URL remains compatible",
			pushURL: "/tmp/native-push-test-repository.git",
			wantURL: "https://github.example.com/team/project/compare/main...review%2Ftopic?expand=1",
		},
		{
			name:    "relative local push URL remains compatible",
			pushURL: "../native-push-test-repository.git",
			wantURL: "https://github.example.com/team/project/compare/main...review%2Ftopic?expand=1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			runNativeTestGit(
				t,
				repoPath,
				"remote",
				"add",
				"publish",
				"https://github.example.com/team/project.git",
			)
			runNativeTestGit(t, repoPath, "config", "remote.publish.pushurl", test.pushURL)
			runNativeTestGit(t, repoPath, "update-ref", "refs/remotes/publish/main", "HEAD")
			runNativeTestGit(
				t,
				repoPath,
				"symbolic-ref",
				"refs/remotes/publish/HEAD",
				"refs/remotes/publish/main",
			)

			target := nativePushTarget{
				branch:    "topic",
				localRef:  "refs/heads/topic",
				remote:    "publish",
				remoteRef: "refs/heads/review/topic",
			}
			if got := gitOps.nativeMergeRequestURL(context.Background(), target); got != test.wantURL {
				t.Fatalf("nativeMergeRequestURL() = %q, want %q", got, test.wantURL)
			}
		})
	}
}

func TestNativeTagLifecycle(t *testing.T) {
	repoPath, gitOps := newNativePushTestRepo(t)
	remotePath := newNativeBareRepo(t)
	configureNativePushRemote(t, repoPath, "origin", remotePath)

	for _, tag := range []string{
		"v1.2.9",
		"v1.10.0",
		"v2.0.0-rc.1",
		"release-v9.0.0",
		"v999999999999999999999999.0.0",
	} {
		runNativeTestGit(t, repoPath, "tag", tag)
	}
	latest, err := gitOps.getLatestTagNative(context.Background())
	if err != nil {
		t.Fatalf("getLatestTagNative() error = %v", err)
	}
	if want := "v999999999999999999999999.0.0"; latest != want {
		t.Fatalf("getLatestTagNative() = %q, want %q", latest, want)
	}

	const (
		tagName = "v3.4.5"
		message = "release heading\n\n-message body without shell interpretation"
	)
	if err := gitOps.createTagNative(context.Background(), tagName, message); err != nil {
		t.Fatalf("createTagNative() error = %v", err)
	}
	if objectType := strings.TrimSpace(string(runNativeTestGit(
		t,
		repoPath,
		"cat-file",
		"-t",
		"refs/tags/"+tagName,
	))); objectType != "tag" {
		t.Fatalf("created object type = %q, want annotated tag", objectType)
	}
	tagObject := string(runNativeTestGit(t, repoPath, "cat-file", "-p", "refs/tags/"+tagName))
	if !strings.Contains(tagObject, "\n\n"+message) {
		t.Fatalf("annotated tag does not contain the exact message; object = %q", tagObject)
	}

	if err := gitOps.pushTagNative(context.Background(), tagName); err != nil {
		t.Fatalf("pushTagNative() error = %v", err)
	}
	localTag := strings.TrimSpace(string(runNativeTestGit(t, repoPath, "rev-parse", "refs/tags/"+tagName)))
	remoteTag := strings.TrimSpace(string(runNativeTestGit(t, remotePath, "rev-parse", "refs/tags/"+tagName)))
	if remoteTag != localTag {
		t.Fatalf("remote tag = %s, want %s", remoteTag, localTag)
	}
	if nativeTestGitSucceeds(remotePath, "show-ref", "--verify", "--quiet", "refs/heads/topic") {
		t.Fatal("pushTagNative() unexpectedly pushed the current branch")
	}
}

func TestNativeTags_ValidateRefsAndMessageFileMode(t *testing.T) {
	_, gitOps := newNativePushTestRepo(t)

	if err := gitOps.createTagNative(context.Background(), "bad..tag", "message"); err == nil ||
		!strings.Contains(err.Error(), "invalid tag name") {
		t.Fatalf("createTagNative(invalid) error = %v", err)
	}
	if err := gitOps.pushTagNative(context.Background(), "missing"); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("pushTagNative(missing) error = %v", err)
	}

	messagePath, cleanup, err := writeNativeTagMessage("private")
	if err != nil {
		t.Fatalf("writeNativeTagMessage() error = %v", err)
	}
	info, err := os.Stat(messagePath)
	if err != nil {
		cleanup()
		t.Fatalf("stat tag message: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		cleanup()
		t.Fatalf("tag message mode = %#o, want 0600", got)
	}
	cleanup()
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("tag message still exists after cleanup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gitOps.createTagNative(ctx, "v8.8.8", "canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("createTagNative(canceled) error = %v, want context.Canceled", err)
	}
}

func TestCreateTagNative_ReconcilesFailureAfterDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX shell wrapper")
	}

	tests := []struct {
		name       string
		mode       string
		want       TagCreationOutcome
		wantExists bool
	}{
		{
			name:       "tag exists after cancellation",
			mode:       "after",
			want:       TagCreationCreated,
			wantExists: true,
		},
		{
			name: "tag is absent after cancellation",
			mode: "before",
			want: TagCreationIndeterminate,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoPath, gitOps := newNativePushTestRepo(t)
			realGit := gitOps.gitPath
			marker := filepath.Join(t.TempDir(), "tag-wrapper-entered")
			wrapper := filepath.Join(t.TempDir(), "git")
			wrapperScript := `#!/bin/sh
if [ "$1" = "tag" ]; then
	if [ "$NATIVE_TAG_TEST_MODE" = "before" ]; then
		: > "$NATIVE_TAG_TEST_MARKER"
		sleep 30
	fi
	"$NATIVE_TAG_TEST_REAL_GIT" "$@"
	rc=$?
	if [ "$rc" -eq 0 ] && [ "$NATIVE_TAG_TEST_MODE" = "after" ]; then
		: > "$NATIVE_TAG_TEST_MARKER"
		sleep 30
	fi
	exit "$rc"
fi
exec "$NATIVE_TAG_TEST_REAL_GIT" "$@"
`
			if err := os.WriteFile(wrapper, []byte(wrapperScript), 0o700); err != nil {
				t.Fatalf("write Git wrapper: %v", err)
			}
			t.Setenv("NATIVE_TAG_TEST_MODE", test.mode)
			t.Setenv("NATIVE_TAG_TEST_MARKER", marker)
			t.Setenv("NATIVE_TAG_TEST_REAL_GIT", realGit)
			gitOps.gitPath = wrapper

			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				result <- gitOps.createTagNative(ctx, "v7.8.9", "reconciled tag")
			}()
			if !waitForNativePushMarker(marker, 5*time.Second) {
				cancel()
				<-result
				t.Fatal("tag wrapper did not reach the cancellation point")
			}
			cancel()
			err := <-result

			var outcome *TagCreationOutcomeError
			if !errors.As(err, &outcome) {
				t.Fatalf(
					"createTagNative() error = %T %v, want TagCreationOutcomeError",
					err,
					err,
				)
			}
			if outcome.Outcome != test.want {
				t.Fatalf("tag outcome = %q, want %q", outcome.Outcome, test.want)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("tag outcome error = %v, want wrapped context.Canceled", err)
			}
			if !strings.Contains(err.Error(), "retry") {
				t.Fatalf("tag outcome error lacks retry warning: %v", err)
			}

			const tagRef = "refs/tags/v7.8.9"
			exists := nativeTestGitSucceeds(
				repoPath,
				"show-ref",
				"--verify",
				"--quiet",
				tagRef,
			)
			if exists != test.wantExists {
				t.Fatalf("tag existence = %v, want %v", exists, test.wantExists)
			}
			if test.wantExists {
				wantOID := strings.TrimSpace(string(runNativeTestGit(
					t,
					repoPath,
					"rev-parse",
					tagRef,
				)))
				if outcome.ObjectID != wantOID {
					t.Fatalf("reconciled tag object = %q, want %q", outcome.ObjectID, wantOID)
				}
			} else if outcome.ObjectID != "" {
				t.Fatalf("indeterminate tag object = %q, want empty", outcome.ObjectID)
			}
		})
	}
}

func newNativePushTestRepo(t *testing.T) (string, *gitOperations) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)

	repoPath := t.TempDir()
	runNativeTestGit(t, repoPath, "init", "-q")
	runNativeTestGit(t, repoPath, "config", "user.name", "Native Push Test")
	runNativeTestGit(t, repoPath, "config", "user.email", "native-push@example.invalid")
	runNativeTestGit(t, repoPath, "config", "commit.gpgsign", "false")
	runNativeTestGit(t, repoPath, "config", "tag.gpgsign", "false")
	runNativeTestGit(t, repoPath, "checkout", "-q", "-b", "topic")
	runNativeTestGit(t, repoPath, "commit", "-q", "--allow-empty", "-m", "initial")

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	return repoPath, &gitOperations{gitPath: gitPath, repoPath: repoPath}
}

func newNativeBareRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	runNativeTestGit(t, repoPath, "init", "--bare", "-q")
	return repoPath
}

func configureNativePushRemote(t *testing.T, repoPath, name, pushURL string) {
	t.Helper()
	configureNativePushRemoteWithURL(t, repoPath, name, pushURL, pushURL)
}

func configureNativePushRemoteWithURL(
	t *testing.T,
	repoPath string,
	name string,
	fetchURL string,
	pushURL string,
) {
	t.Helper()
	runNativeTestGit(t, repoPath, "remote", "add", name, fetchURL)
	if pushURL != fetchURL {
		runNativeTestGit(t, repoPath, "config", "remote."+name+".pushurl", pushURL)
	}
}

func configureNativeUpstream(t *testing.T, repoPath, remote, mergeRef string) {
	t.Helper()
	runNativeTestGit(t, repoPath, "config", "branch.topic.remote", remote)
	runNativeTestGit(t, repoPath, "config", "branch.topic.merge", mergeRef)
}

func assertNativePushRemote(t *testing.T, gitOps *gitOperations, want string) {
	t.Helper()
	got, err := gitOps.resolveNativePushRemote(context.Background(), "topic")
	if err != nil {
		t.Fatalf("resolveNativePushRemote() error = %v", err)
	}
	if got != want {
		t.Fatalf("resolveNativePushRemote() = %q, want %q", got, want)
	}
}

func runNativeTestGit(t *testing.T, repoPath string, args ...string) []byte {
	t.Helper()
	gitArgs := append([]string{"-C", repoPath}, args...)
	command := exec.Command("git", gitArgs...)
	command.Env = sanitizedGitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output
}

func nativeTestGitSucceeds(repoPath string, args ...string) bool {
	gitArgs := append([]string{"-C", repoPath}, args...)
	command := exec.Command("git", gitArgs...)
	command.Env = sanitizedGitEnvironment(os.Environ())
	return command.Run() == nil
}

func waitForNativePushMarker(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
