package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Push configuration", func() {
	DescribeTable("pushes only the checked-out branch despite Git defaults",
		func(ctx SpecContext, mode, upstream string, autoSetup bool) {
			repository := newPushRepository()
			remote := newBareRemote(repository.GlobalConfig)
			repository.git("remote", "add", "origin", remote)
			repository.git("config", "push.default", mode)
			repository.git("config", "push.followTags", "true")
			repository.git("tag", "-a", "v7.7.7", "-m", "reachable tag")
			repository.git("branch", "other", "HEAD")
			if upstream != "" {
				repository.git("config", "branch.topic.remote", "origin")
				repository.git("config", "branch.topic.merge", upstream)
			}
			if autoSetup {
				repository.git("config", "push.autoSetupRemote", "true")
			}
			result := runCLI(ctx, repository.Path, repositoryOptions(repository),
				"--auto", "--providers=openai", "--push")
			Expect(result.ExitCode).To(Equal(0), result.Output())
			Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/topic\n"))
			if autoSetup {
				Expect(repository.git("config", "branch.topic.remote")).To(Equal("origin\n"))
				Expect(repository.git("config", "branch.topic.merge")).To(Equal("refs/heads/topic\n"))
			}
		},
		Entry("current without upstream", "current", "", false),
		Entry("upstream pointing elsewhere", "upstream", "refs/heads/review/topic", false),
		Entry("tracking pointing elsewhere", "tracking", "refs/heads/review/topic", false),
		Entry("simple with matching upstream", "simple", "refs/heads/topic", false),
		Entry("simple with different upstream", "simple", "refs/heads/review/topic", false),
		Entry("simple without upstream", "simple", "", false),
		Entry("automatic upstream setup", "simple", "", true),
		Entry("nothing", "nothing", "", false),
		Entry("matching", "matching", "", false),
	)

	DescribeTable("ignores configured push refspecs",
		func(ctx SpecContext, branch string, refspecs []string) {
			repository := newPushRepository()
			if branch != "topic" {
				repository.git("update-ref", "refs/heads/"+branch, "HEAD")
				repository.git("symbolic-ref", "HEAD", "refs/heads/"+branch)
			}
			remote := newBareRemote(repository.GlobalConfig)
			repository.git("remote", "add", "origin", remote)
			repository.git("config", "push.default", "nothing")
			repository.git("tag", "topic")
			repository.git("branch", "other", "HEAD")
			for _, refspec := range refspecs {
				repository.git("config", "--add", "remote.origin.push", refspec)
			}
			result := runCLI(ctx, repository.Path, repositoryOptions(repository),
				"--auto", "--providers=openai", "--push")
			Expect(result.ExitCode).To(Equal(0), result.Output())
			Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/" + branch + "\n"))
		},
		Entry("full destination", "topic", []string{"HEAD:refs/heads/review/topic"}),
		Entry("ambiguous short source", "topic", []string{"topic:published"}),
		Entry("qualified source", "topic", []string{"refs/heads/topic:published"}),
		Entry("omitted destination", "topic", []string{"refs/heads/topic"}),
		Entry("force", "topic", []string{"+HEAD:refs/heads/forced"}),
		Entry("matching", "topic", []string{":"}),
		Entry("wildcard", "topic", []string{"refs/heads/*:refs/heads/*"}),
		Entry("negative", "topic", []string{"^refs/heads/other"}),
		Entry("multiple", "topic", []string{"HEAD:refs/heads/one", "HEAD:refs/heads/two"}),
		Entry("different source", "topic", []string{"refs/heads/other:refs/heads/topic"}),
		Entry("deletion", "topic", []string{":refs/heads/topic"}),
		Entry("tag destination", "topic", []string{"HEAD:refs/tags/topic"}),
		Entry("HEAD destination", "topic", []string{"HEAD:HEAD"}),
		Entry("option-like branch", "-topic", []string{"-topic:published"}),
	)

	DescribeTable("selects the configured remote in precedence order",
		func(ctx SpecContext, branchPush, defaultPush, pull, expected string) {
			repository := newPushRepository()
			remotes := map[string]string{}
			for _, name := range []string{"pull", "default-push", "branch-push"} {
				remotes[name] = newBareRemote(repository.GlobalConfig)
				repository.git("remote", "add", name, remotes[name])
			}
			for key, value := range map[string]string{
				"branch.topic.pushRemote": branchPush,
				"remote.pushDefault":      defaultPush,
				"branch.topic.remote":     pull,
			} {
				if value != "" {
					repository.git("config", key, value)
				}
			}
			result := runCLI(ctx, repository.Path, repositoryOptions(repository),
				"--auto", "--providers=openai", "--push")
			if expected == "" {
				Expect(result.ExitCode).To(Equal(1))
				Expect(result.Output()).To(ContainSubstring("multiple remotes"))
			} else {
				Expect(result.ExitCode).To(Equal(0), result.Output())
			}
			for name, path := range remotes {
				refs := repository.git("ls-remote", path)
				if name == expected {
					Expect(refs).To(Equal(repository.head() + "\trefs/heads/topic\n"))
				} else {
					Expect(refs).To(BeEmpty())
				}
			}
		},
		Entry("branch pushRemote wins", "branch-push", "default-push", "pull", "branch-push"),
		Entry("pushDefault wins over pull remote", "", "default-push", "pull", "default-push"),
		Entry("pull remote is the fallback", "", "", "pull", "pull"),
		Entry("triangular workflow without pull configuration", "branch-push", "", "", "branch-push"),
		Entry("unconfigured multiple remotes are ambiguous", "", "", "", ""),
	)

	It("falls back to a named remote when the branch tracks a local branch", func(ctx SpecContext) {
		repository := newPushRepository()
		remote := newBareRemote(repository.GlobalConfig)
		repository.git("remote", "add", "origin", remote)
		repository.git("config", "branch.topic.remote", ".")
		repository.git("config", "branch.topic.merge", "refs/heads/master")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository),
			"--auto", "--providers=openai", "--push")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/topic\n"))
	})

	DescribeTable("refuses unsafe push targets before dispatch",
		func(ctx SpecContext, detached bool, expectedError string) {
			repository := newPushRepository()
			remote := newBareRemote(repository.GlobalConfig)
			repository.git("remote", "add", "origin", remote)
			if detached {
				repository.git("checkout", "--detach")
			} else {
				repository.git("config", "remote.origin.mirror", "true")
			}
			result := runCLI(ctx, repository.Path, repositoryOptions(repository),
				"--auto", "--providers=openai", "--push")
			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring(expectedError))
			Expect(result.Output()).NotTo(ContainSubstring("remote may have updated"))
			Expect(repository.git("ls-remote", remote)).To(BeEmpty())
		},
		Entry("detached HEAD", true, "detached HEAD"),
		Entry("mirror remote", false, "configured as a mirror"),
	)

	It("reports an indeterminate outcome after one of multiple push URLs succeeds", func(ctx SpecContext) {
		repository := newPushRepository()
		remote := newBareRemote(repository.GlobalConfig)
		missing := filepath.Join(GinkgoT().TempDir(), "missing.git")
		repository.git("remote", "add", "publish", remote)
		repository.git("config", "--add", "remote.publish.pushurl", remote)
		repository.git("config", "--add", "remote.publish.pushurl", missing)
		result := runCLI(ctx, repository.Path, repositoryOptions(repository),
			"--auto", "--providers=openai", "--push")
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("remote may have updated"))
		Expect(result.Output()).NotTo(ContainSubstring("context canceled"))
		Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/topic\n"))
	})

	DescribeTable("reports review links using the effective push repository",
		func(ctx SpecContext, transport string, wantLink bool) {
			repository := newPushRepository()
			remote := newBareRemote(repository.GlobalConfig)
			const fetchURL = "https://github.example.test/team/project.git"
			repository.git("remote", "add", "publish", fetchURL)
			repository.git("config", "branch.topic.remote", "publish")
			repository.git("config", "branch.topic.merge", "refs/heads/review/upstream-topic")
			repository.git("config", "push.default", "upstream")
			repository.git("config", "remote.publish.push", "HEAD:refs/heads/review/topic")
			repository.git("update-ref", "refs/remotes/publish/main", "HEAD")
			repository.git("symbolic-ref", "refs/remotes/publish/HEAD", "refs/remotes/publish/main")
			options := repositoryOptions(repository)
			switch transport {
			case "local":
				repository.git("config", "remote.publish.pushurl", remote)
			case "relative":
				relative, err := filepath.Rel(repository.Path, remote)
				Expect(err).NotTo(HaveOccurred())
				repository.git("config", "remote.publish.pushurl", relative)
			default:
				if runtime.GOOS == "windows" {
					Skip("the local SSH transport fixture uses POSIX shell")
				}
				ssh := filepath.Join(GinkgoT().TempDir(), "ssh")
				Expect(
					os.WriteFile(ssh, []byte("#!/bin/sh\nexec git receive-pack \"$E2E_REMOTE\"\n"), 0o700),
				).To(Succeed())
				options.Environment = map[string]string{"GIT_SSH": ssh, "GIT_SSH_VARIANT": "ssh", "E2E_REMOTE": remote}
				pushURL := "ssh://deploy@github.example.test/team/project.git"
				if transport != "same repository" {
					pushURL = "ssh://git@github.example.test/other/project.git"
				}
				if transport == "pushInsteadOf" {
					repository.git("config", "url."+pushURL+".pushInsteadOf", fetchURL)
				} else {
					repository.git("config", "remote.publish.pushurl", pushURL)
				}
			}
			result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--push")
			Expect(result.ExitCode).To(Equal(0), result.Output())
			Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/topic\n"))
			link := "https://github.example.test/team/project/compare/main...topic?expand=1"
			if wantLink {
				Expect(result.Output()).To(ContainSubstring(link))
			} else {
				Expect(result.Output()).NotTo(ContainSubstring("/compare/"))
			}
		},
		Entry("absolute local transport", "local", true),
		Entry("relative local transport", "relative", true),
		Entry("same repository over SSH", "same repository", true),
		Entry("different hosted repository", "different repository", false),
		Entry("pushInsteadOf changes the repository", "pushInsteadOf", false),
	)
})

func newPushRepository() *gitRepository {
	GinkgoHelper()
	repository := newRepository()
	repository.git("switch", "-c", "topic")
	repository.git("config", "tag.gpgsign", "false")
	repository.append("tracked.txt", "pushed change\n")
	return repository
}
