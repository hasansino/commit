package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Staging and commit behavior", func() {
	It("commits tracked and untracked changes through the executable", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "tracked change\n")
		repository.write("new.txt", "new file\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat(files): commit all changes"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("Commit created"))
		Expect(repository.commitCount()).To(Equal("2"))
		Expect(repository.commitMessage()).To(Equal("feat(files): commit all changes"))
		Expect(strings.Fields(repository.git("show", "--format=", "--name-only", "HEAD"))).To(
			ConsistOf("new.txt", "tracked.txt"),
		)
		Expect(repository.git("log", "-1", "--format=%an <%ae>")).To(Equal("E2E User <e2e@example.test>\n"))
		Expect(repository.status()).To(BeEmpty())
	})

	It("commits tracked renames and deletions", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("old-name.txt", "content that will keep its identity across a rename\n")
		repository.write("deleted.txt", "content that will be deleted\n")
		repository.git("add", "--", "old-name.txt", "deleted.txt")
		repository.git("commit", "--no-gpg-sign", "-m", "test: seed tracked paths")
		Expect(os.Rename(
			filepath.Join(repository.Path, "old-name.txt"),
			filepath.Join(repository.Path, "renamed.txt"),
		)).To(Succeed())
		Expect(os.Remove(filepath.Join(repository.Path, "deleted.txt"))).To(Succeed())
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "refactor(files): rename and remove tracked paths"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(strings.Fields(repository.git("ls-tree", "-r", "--name-only", "HEAD"))).To(
			ConsistOf("renamed.txt", "tracked.txt"),
		)
		nameStatus := repository.git("show", "--format=", "--name-status", "--find-renames", "HEAD")
		Expect(nameStatus).To(ContainSubstring("D\tdeleted.txt"))
		Expect(nameStatus).To(ContainSubstring("R100\told-name.txt\trenamed.txt"))
		Expect(repository.status()).To(BeEmpty())
		requests := api.requestsFor(providerOpenAI)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Prompt).To(ContainSubstring("deleted.txt"))
		Expect(requests[0].Prompt).To(ContainSubstring("renamed.txt"))
	})

	It("restores the exact index after a dry run", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "dry-run change\n")
		repository.write("untracked.txt", "also dry-run\n")
		beforeStatus := repository.status()
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--dry-run",
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("Dry run enabled"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.status()).To(Equal(beforeStatus))
		requests := api.requestsFor(providerOpenAI)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Prompt).To(ContainSubstring("tracked.txt"))
		Expect(requests[0].Prompt).To(ContainSubstring("untracked.txt"))
	})

	It("combines include and exclude selectors with exclude taking precedence", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("src/include.go", "package include\n")
		repository.write("src/exclude.go", "package exclude\n")
		repository.write("notes.md", "notes\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat(src): add included source"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--include-only=src/*.go",
			"--exclude=src/exclude.go",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.git("show", "--format=", "--name-only", "HEAD")).To(Equal("src/include.go\n"))
		Expect(repository.status()).To(ContainSubstring("?? notes.md"))
		Expect(repository.status()).To(ContainSubstring("?? src/exclude.go"))
		prompt := api.requestsFor(providerOpenAI)[0].Prompt
		Expect(prompt).To(ContainSubstring("src/include.go"))
		Expect(prompt).NotTo(ContainSubstring("src/exclude.go"))
		Expect(prompt).NotTo(ContainSubstring("notes.md"))
	})

	It("applies a basename glob to files in nested directories", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("nested/source.go", "package nested\n")
		repository.write("nested/readme.md", "not selected\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--include-only=*.go",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(api.requestsFor(providerOpenAI)).To(HaveLen(1))
		Expect(repository.git("show", "--format=", "--name-only", "HEAD")).To(Equal("nested/source.go\n"))
		Expect(repository.status()).To(ContainSubstring("?? nested/readme.md"))
	})

	DescribeTable("honors the configured global ignore policy",
		func(ctx SpecContext, disableGlobalIgnore bool, expectedFiles []string) {
			repository := newRepository()
			ignoreFile := filepath.Join(filepath.Dir(repository.Path), "global-ignore")
			Expect(os.WriteFile(ignoreFile, []byte("*.log\n!important.log\n"), 0o600)).To(Succeed())
			repository.git("config", "--global", "core.excludesFile", ignoreFile)
			repository.write("ignored.log", "ignored\n")
			repository.write("important.log", "re-included\n")
			repository.write("source.txt", "source\n")
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig
			arguments := []string{"--auto", "--providers=openai"}
			if disableGlobalIgnore {
				arguments = append(arguments, "--use-global-gitignore=false")
			}

			result := runCLI(ctx, repository.Path, options, arguments...)

			Expect(result.ExitCode).To(Equal(0))
			changed := strings.Fields(repository.git("show", "--format=", "--name-only", "HEAD"))
			Expect(changed).To(ConsistOf(expectedFiles))
		},
		Entry("enabled by default", false, []string{"important.log", "source.txt"}),
		Entry("explicitly disabled", true, []string{"ignored.log", "important.log", "source.txt"}),
	)

	It("uses an existing partially staged set exactly", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("tracked.txt", "initial\nstaged version\n")
		repository.git("add", "--", "tracked.txt")
		repository.append("tracked.txt", "unstaged version\n")
		repository.write("unrelated.txt", "must remain untracked\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "fix(index): commit staged content"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--include-only=unrelated.txt",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("include and exclude settings were not applied"))
		Expect(repository.git("show", "HEAD:tracked.txt")).To(Equal("initial\nstaged version\n"))
		Expect(repository.read("tracked.txt")).To(Equal("initial\nstaged version\nunstaged version\n"))
		Expect(repository.status()).To(ContainSubstring(" M tracked.txt"))
		Expect(repository.status()).To(ContainSubstring("?? unrelated.txt"))
		prompt := api.requestsFor(providerOpenAI)[0].Prompt
		Expect(prompt).To(ContainSubstring("staged version"))
		Expect(prompt).NotTo(ContainSubstring("unstaged version"))
		Expect(prompt).NotTo(ContainSubstring("unrelated.txt"))
	})

	It("preserves an existing index exactly on a dry run", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "already staged\n")
		repository.git("add", "--", "tracked.txt")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--dry-run", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.cachedDiff()).To(ContainSubstring("already staged"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	It("rejects selector negation without mutating the index", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "selector failure\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--exclude=!tracked.txt",
		)

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("do not support negation"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	It("rejects intent-to-add entries without losing them", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("intent.txt", "intent content\n")
		repository.git("add", "--intent-to-add", "--", "intent.txt")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("intent-to-add entries are not supported safely"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.git("diff", "--name-only")).To(ContainSubstring("intent.txt"))
	})

	It("runs from a nested working directory and commits relative to the repository root", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "root-level change\n")
		repository.write("nested/new.txt", "nested change\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, filepath.Join(repository.Path, "nested"), options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(strings.Fields(repository.git("show", "--format=", "--name-only", "HEAD"))).To(
			ConsistOf("nested/new.txt", "tracked.txt"),
		)
		Expect(repository.status()).To(BeEmpty())
	})

	It("creates the first commit on an unborn branch", func(ctx SpecContext) {
		repository := newUnbornRepository()
		repository.write("first.txt", "first commit\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat: create repository history"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.commitCount()).To(Equal("1"))
		Expect(repository.commitMessage()).To(Equal("feat: create repository history"))
		Expect(repository.git("show", "--format=", "--name-only", "HEAD")).To(Equal("first.txt\n"))
	})

	It("handles filenames that look like command options", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("--literal path.txt", "literal path\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.git("ls-tree", "--name-only", "HEAD")).To(ContainSubstring("--literal path.txt"))
	})

	DescribeTable("restores temporary staging when Git refuses the commit",
		func(ctx SpecContext, configureFailure func(*gitRepository), expectedError string) {
			repository := newRepository()
			repository.append("tracked.txt", "commit should fail\n")
			beforeIndex := repository.indexBytes()
			configureFailure(repository)
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring(expectedError))
			Expect(api.requestsFor(providerOpenAI)).To(HaveLen(1))
			Expect(repository.indexBytes()).To(Equal(beforeIndex))
			Expect(repository.head()).To(Equal(repository.InitialHead))
		},
		Entry("the author email is missing", func(repository *gitRepository) {
			repository.git("config", "user.useConfigOnly", "true")
			repository.git("config", "--unset", "user.email")
		}, "no email was given and auto-detection is disabled"),
		Entry("signing is enabled without a key", func(repository *gitRepository) {
			repository.git("config", "commit.gpgsign", "true")
			_, _ = repository.tryGit("config", "--unset", "user.signingkey")
		}, "failed to write commit object"),
	)

	It("returns successfully when selectors match no changes", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "not selected\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto",
			"--providers=openai",
			"--include-only=missing.txt",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("No files to commit"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.status()).To(ContainSubstring(" M tracked.txt"))
	})
})

var _ = Describe("Concurrent repository changes", func() {
	It("does not overwrite an index changed while the provider is responding", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "temporary staged change\n")
		release := make(chan struct{})
		releaseProvider := sync.OnceFunc(func() { close(release) })
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat: stale suggestion", Release: release})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		session := startCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		DeferCleanup(releaseProvider)
		Eventually(func() int {
			return len(api.requestsFor(providerOpenAI))
		}).WithContext(ctx).WithTimeout(commandTimeout).Should(Equal(1))

		repository.write("external.txt", "staged by another process\n")
		repository.git("add", "--", "external.txt")
		releaseProvider()
		result := resultFromSession(ctx, session, commandTimeout)

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("git index changed while preparing the commit"))
		Expect(repository.cachedDiff()).To(ContainSubstring("staged by another process"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	It("does not overwrite HEAD when another process advances the branch", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "temporary staged change\n")
		release := make(chan struct{})
		releaseProvider := sync.OnceFunc(func() { close(release) })
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat: stale suggestion", Release: release})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		session := startCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		DeferCleanup(releaseProvider)
		Eventually(func() int {
			return len(api.requestsFor(providerOpenAI))
		}).WithContext(ctx).WithTimeout(commandTimeout).Should(Equal(1))

		oldHead := repository.head()
		tree := strings.TrimSpace(repository.git("rev-parse", oldHead+"^{tree}"))
		externalHead := strings.TrimSpace(repository.git(
			"commit-tree",
			tree,
			"-p",
			oldHead,
			"-m",
			"test: external branch update",
		))
		repository.git("update-ref", "HEAD", externalHead, oldHead)
		releaseProvider()
		result := resultFromSession(ctx, session, commandTimeout)

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("HEAD changed while preparing the commit"))
		Expect(repository.head()).To(Equal(externalHead))
		Expect(api.requestsFor(providerOpenAI)).To(HaveLen(1))
	})
})
