package e2e_test

import (
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Version tags", func() {
	It("selects and increments tags larger than a machine integer", func(ctx SpecContext) {
		repository := newRepository()
		repository.git("config", "tag.gpgsign", "false")
		for _, tag := range []string{
			"v1.2.9", "v1.10.0", "v2.0.0-rc.1", "release-v9.0.0", "v999999999999999999999999.0.0",
		} {
			repository.git("tag", tag)
		}
		repository.append("tracked.txt", "large release version\n")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository),
			"--auto", "--providers=openai", "--tag=major")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("rev-parse", "refs/tags/v1000000000000000000000000.0.0^{}")).
			To(Equal(repository.head() + "\n"))
	})

	It("preserves the exact multiline message in an annotated tag and pushes that object", func(ctx SpecContext) {
		repository := newPushRepository()
		remote := newBareRemote(repository.GlobalConfig)
		repository.git("remote", "add", "origin", remote)
		message := "release heading\n\n-message body without shell interpretation"
		options := repositoryOptions(repository)
		options.API.setReply(providerOpenAI, apiReply{Message: message})
		result := runCLI(ctx, repository.Path, options,
			"--auto", "--providers=openai", "--multi-line", "--push", "--tag=patch")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("cat-file", "-t", "refs/tags/v0.0.1")).To(Equal("tag\n"))
		Expect(repository.git("cat-file", "-p", "refs/tags/v0.0.1")).To(HaveSuffix("\n\n" + message + "\n"))
		objectID := strings.TrimSpace(repository.git("rev-parse", "refs/tags/v0.0.1"))
		Expect(repository.git("ls-remote", remote, "refs/tags/v0.0.1")).
			To(Equal(objectID + "\trefs/tags/v0.0.1\n"))
	})

	DescribeTable("creates the first semantic version tag",
		func(ctx SpecContext, increment, expectedTag string) {
			repository := newRepository()
			repository.append("tracked.txt", increment+" release\n")
			api := newFakeAI()
			api.setReply(providerOpenAI, apiReply{Message: "chore(release): create " + increment + " version"})
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(
				ctx,
				repository.Path,
				options,
				"--auto",
				"--providers=openai",
				"--tag="+increment,
			)

			Expect(result.ExitCode).To(Equal(0))
			Expect(result.Output()).To(ContainSubstring("Tag created"))
			Expect(strings.Fields(repository.git("tag", "--list"))).To(ConsistOf(expectedTag))
			Expect(strings.TrimSpace(repository.git("rev-list", "-n", "1", expectedTag))).To(Equal(repository.head()))
			objectType := repository.git(
				"for-each-ref",
				"--format=%(objecttype)",
				"refs/tags/"+expectedTag,
			)
			Expect(strings.TrimSpace(objectType)).To(Equal("tag"))
		},
		Entry("major", "major", "v1.0.0"),
		Entry("minor", "minor", "v0.1.0"),
		Entry("patch", "patch", "v0.0.1"),
	)

	It("increments the numerically greatest valid tag and ignores other names", func(ctx SpecContext) {
		repository := newRepository()
		repository.git("tag", "v1.9.9")
		repository.git("tag", "v1.10.0")
		repository.git("tag", "release-v99.0.0")
		repository.git("tag", "v2.0")
		repository.append("tracked.txt", "next patch\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--tag=patch")

		Expect(result.ExitCode).To(Equal(0))
		Expect(strings.Fields(repository.git("tag", "--list"))).To(ContainElement("v1.10.1"))
		Expect(strings.TrimSpace(repository.git("rev-list", "-n", "1", "v1.10.1"))).To(Equal(repository.head()))
	})

	It("does not create a commit or tag in dry-run mode", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "dry release\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--dry-run",
			"--push",
			"--tag=major",
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.git("tag", "--list")).To(BeEmpty())
	})
})

var _ = Describe("Push behavior", func() {
	DescribeTable("pushes to a local transport while reporting the hosted review URL",
		func(ctx SpecContext, hostedURL, expectedURL string) {
			repository := newRepository()
			bareRemote := newBareRemote(repository.GlobalConfig)
			repository.git("remote", "add", "origin", bareRemote)
			repository.git("push", "--set-upstream", "origin", "master")
			repository.git("remote", "set-head", "origin", "master")
			repository.git("remote", "set-url", "origin", hostedURL)
			repository.git("config", "remote.origin.pushurl", bareRemote)
			branch := "feature/APP-42-review-link"
			repository.git("switch", "-c", branch)
			repository.git("config", "push.default", "current")
			repository.append("tracked.txt", "push and tag\n")
			api := newFakeAI()
			api.setReply(providerOpenAI, apiReply{Message: "feat(push): publish review branch"})
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(
				ctx,
				repository.Path,
				options,
				"--auto",
				"--providers=openai",
				"--push",
				"--tag=patch",
			)

			Expect(result.ExitCode).To(Equal(0))
			Expect(result.Output()).To(ContainSubstring("Successfully pushed"))
			Expect(result.Output()).To(ContainSubstring(expectedURL))
			Expect(result.Output()).To(ContainSubstring("Tag pushed to remote"))
			remoteBranch := strings.TrimSpace(repository.git("ls-remote", bareRemote, "refs/heads/"+branch))
			Expect(remoteBranch).To(HavePrefix(repository.head()))
			remoteTag := strings.TrimSpace(repository.git("ls-remote", bareRemote, "refs/tags/v0.0.1^{}"))
			Expect(remoteTag).To(HavePrefix(repository.head()))
		},
		Entry(
			"for GitHub",
			"https://github.example.test/acme/widgets.git",
			"https://github.example.test/acme/widgets/compare/master..."+url.QueryEscape(
				"feature/APP-42-review-link",
			)+"?expand=1",
		),
		Entry(
			"for a nested GitLab group",
			"https://gitlab.example.test/acme/platform/widgets.git",
			"https://gitlab.example.test/acme/platform/widgets/-/merge_requests/new?"+
				url.Values{
					"merge_request[source_branch]": {"feature/APP-42-review-link"},
					"merge_request[target_branch]": {"master"},
				}.Encode(),
		),
	)

	It("honors the branch pushRemote configuration", func(ctx SpecContext) {
		repository := newRepository()
		origin := newBareRemote(repository.GlobalConfig)
		backup := newBareRemote(repository.GlobalConfig)
		repository.git("remote", "add", "origin", origin)
		repository.git("remote", "add", "backup", backup)
		repository.git("switch", "-c", "feature/push-remote")
		repository.git("config", "branch.feature/push-remote.pushRemote", "backup")
		repository.append("tracked.txt", "configured remote\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--push")

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.git("ls-remote", backup, "refs/heads/feature/push-remote")).To(HavePrefix(repository.head()))
		Expect(repository.git("ls-remote", origin, "refs/heads/feature/push-remote")).To(BeEmpty())
	})

	It("reports a push failure after retaining the local commit", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "local commit before push failure\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "fix(push): retain local work"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--push")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("failed to push"))
		Expect(repository.commitCount()).To(Equal("2"))
		Expect(repository.commitMessage()).To(Equal("fix(push): retain local work"))
		Expect(repository.status()).To(BeEmpty())
	})
})
