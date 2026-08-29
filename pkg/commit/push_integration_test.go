package commit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func TestPushRemoteNamePrecedence(t *testing.T) {
	isolatePushIntegrationGitConfig(t)
	repoPath := newStagingIntegrationRepo(t)
	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	branch, err := gitOps.GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}

	branchRemoteKey := fmt.Sprintf("branch.%s.remote", branch)
	branchPushRemoteKey := fmt.Sprintf("branch.%s.pushRemote", branch)
	runStagingIntegrationGit(t, repoPath, nil, "config", branchRemoteKey, "pull-remote")
	runStagingIntegrationGit(t, repoPath, nil, "config", "remote.pushDefault", "default-push")
	runStagingIntegrationGit(t, repoPath, nil, "config", branchPushRemoteKey, "branch-push")

	assertPushRemoteName(t, gitOps, branch, "branch-push")
	runStagingIntegrationGit(t, repoPath, nil, "config", "--unset", branchPushRemoteKey)
	assertPushRemoteName(t, gitOps, branch, "default-push")
	runStagingIntegrationGit(t, repoPath, nil, "config", "--unset", "remote.pushDefault")
	assertPushRemoteName(t, gitOps, branch, "pull-remote")
	runStagingIntegrationGit(t, repoPath, nil, "config", "--unset", branchRemoteKey)
	runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"remote",
		"add",
		"sole-remote",
		"https://example.invalid/sole.git",
	)
	assertPushRemoteName(t, gitOps, branch, "sole-remote")
	runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"remote",
		"add",
		"origin",
		"https://example.invalid/origin.git",
	)
	assertPushRemoteName(t, gitOps, branch, "origin")
}

func TestPushRemoteNameUsesSoleRemoteFromGlobalConfig(t *testing.T) {
	isolatePushIntegrationGitConfig(t)
	repoPath := newStagingIntegrationRepo(t)
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	configContents := "[remote \"global-only\"]\n\turl = https://example.invalid/global.git\n"
	if err := os.WriteFile(globalConfig, []byte(configContents), 0o600); err != nil {
		t.Fatalf("write global Git config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	branch, err := gitOps.GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch() error = %v", err)
	}
	assertPushRemoteName(t, gitOps, branch, "global-only")
	remoteURL, err := gitOps.GetRemoteURL("global-only")
	if err != nil {
		t.Fatalf("GetRemoteURL() error = %v", err)
	}
	if want := "https://example.invalid/global.git"; remoteURL != want {
		t.Fatalf("GetRemoteURL() = %q, want %q", remoteURL, want)
	}
}

func TestPushIntegration_UsesConfiguredRemoteAndDefaultBranch(t *testing.T) {
	isolatePushIntegrationGitConfig(t)
	repoPath := newStagingIntegrationRepo(t)
	runStagingIntegrationGit(t, repoPath, nil, "checkout", "-q", "-b", "feature")

	originPath := newPushIntegrationBareRepo(t)
	publishPath := newPushIntegrationBareRepo(t)
	configurePushIntegrationRemote(
		t,
		repoPath,
		"origin",
		"https://github.example.com/team/decoy.git",
		originPath,
	)
	configurePushIntegrationRemote(
		t,
		repoPath,
		"publish",
		"https://gitlab.example.com/group/project.git",
		publishPath,
	)
	runStagingIntegrationGit(t, repoPath, nil, "config", "branch.feature.remote", "origin")
	runStagingIntegrationGit(t, repoPath, nil, "config", "remote.pushDefault", "origin")
	runStagingIntegrationGit(t, repoPath, nil, "config", "branch.feature.pushRemote", "publish")

	// Seed the selected remote's default branch and its local remote-HEAD metadata.
	runStagingIntegrationGit(t, repoPath, nil, "push", publishPath, "HEAD:refs/heads/main")
	runStagingIntegrationGit(t, repoPath, nil, "update-ref", "refs/remotes/publish/main", "HEAD")
	runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"symbolic-ref",
		"refs/remotes/publish/HEAD",
		"refs/remotes/publish/main",
	)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	mrURL, err := gitOps.Push()
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	wantURL := "https://gitlab.example.com/group/project/-/merge_requests/new?merge_request%5Bsource_branch%5D=feature&merge_request%5Btarget_branch%5D=main"
	if mrURL != wantURL {
		t.Fatalf("Push() URL = %q, want %q", mrURL, wantURL)
	}

	localHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"rev-parse",
		"HEAD",
	)))
	assertPushIntegrationRef(t, publishPath, "refs/heads/feature", localHead)
	assertPushIntegrationRefMissing(t, originPath, "refs/heads/feature")

	const tagName = "l3-integration-tag"
	runStagingIntegrationGit(t, repoPath, nil, "tag", tagName)
	if err := gitOps.PushTag(tagName); err != nil {
		t.Fatalf("PushTag() error = %v", err)
	}
	assertPushIntegrationRef(t, publishPath, "refs/tags/"+tagName, localHead)
	assertPushIntegrationRefMissing(t, originPath, "refs/tags/"+tagName)
}

func TestPushIntegration_UnknownDefaultBranchIsNotMaster(t *testing.T) {
	isolatePushIntegrationGitConfig(t)
	repoPath := newStagingIntegrationRepo(t)
	runStagingIntegrationGit(t, repoPath, nil, "checkout", "-q", "-b", "feature-no-head")

	publishPath := newPushIntegrationBareRepo(t)
	configurePushIntegrationRemote(
		t,
		repoPath,
		"publish",
		"https://github.example.com/team/project.git",
		publishPath,
	)

	gitOps := newStagingIntegrationGitOperations(t, repoPath)
	if got := gitOps.GetDefaultBranch("publish"); got != "" {
		t.Fatalf("GetDefaultBranch() = %q, want empty", got)
	}

	mrURL, err := gitOps.Push()
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	wantURL := "https://github.example.com/team/project/pull/new/feature-no-head"
	if mrURL != wantURL {
		t.Fatalf("Push() URL = %q, want %q", mrURL, wantURL)
	}

	const tagName = "l3-sole-remote-tag"
	runStagingIntegrationGit(t, repoPath, nil, "tag", tagName)
	if err := gitOps.PushTag(tagName); err != nil {
		t.Fatalf("PushTag() error = %v", err)
	}
	localHead := strings.TrimSpace(string(runStagingIntegrationGit(
		t,
		repoPath,
		nil,
		"rev-parse",
		"HEAD",
	)))
	assertPushIntegrationRef(t, publishPath, "refs/tags/"+tagName, localHead)
}

func isolatePushIntegrationGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
}

func newPushIntegrationBareRepo(t *testing.T) string {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "remote.git")
	if err := os.Mkdir(repoPath, 0o755); err != nil {
		t.Fatalf("create bare repository directory: %v", err)
	}
	runStagingIntegrationGit(t, repoPath, nil, "init", "--bare", "-q")
	return repoPath
}

func configurePushIntegrationRemote(
	t *testing.T,
	repoPath, name, fetchURL, pushURL string,
) {
	t.Helper()
	runStagingIntegrationGit(t, repoPath, nil, "remote", "add", name, fetchURL)
	runStagingIntegrationGit(t, repoPath, nil, "config", "remote."+name+".pushurl", pushURL)
}

func assertPushRemoteName(
	t *testing.T,
	gitOps *gitOperations,
	branch, want string,
) {
	t.Helper()
	got, err := gitOps.getPushRemoteName(branch)
	if err != nil {
		t.Fatalf("getPushRemoteName(%q) error = %v", branch, err)
	}
	if got != want {
		t.Fatalf("getPushRemoteName(%q) = %q, want %q", branch, got, want)
	}
}

func assertPushIntegrationRef(
	t *testing.T,
	repoPath, refName, wantHash string,
) {
	t.Helper()
	repository, err := git.PlainOpen(repoPath)
	if err != nil {
		t.Fatalf("open bare repository: %v", err)
	}
	reference, err := repository.Reference(plumbing.ReferenceName(refName), true)
	if err != nil {
		t.Fatalf("read %s: %v", refName, err)
	}
	if got := reference.Hash().String(); got != wantHash {
		t.Fatalf("%s = %s, want %s", refName, got, wantHash)
	}
}

func assertPushIntegrationRefMissing(t *testing.T, repoPath, refName string) {
	t.Helper()
	repository, err := git.PlainOpen(repoPath)
	if err != nil {
		t.Fatalf("open bare repository: %v", err)
	}
	_, err = repository.Reference(plumbing.ReferenceName(refName), true)
	if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		t.Fatalf("Reference(%q) error = %v, want reference not found", refName, err)
	}
}
