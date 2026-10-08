package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
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
		Expect(privateStagingFiles(repository)).To(BeEmpty())
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
		Expect(privateStagingFiles(repository)).To(BeEmpty())
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
			ignoreRules := "*.log\n!important.log\ngenerated/**\n!generated/keep.txt\n"
			Expect(os.WriteFile(ignoreFile, []byte(ignoreRules), 0o600)).To(Succeed())
			repository.git("config", "--global", "core.excludesFile", ignoreFile)
			repository.write("ignored.log", "ignored\n")
			repository.write("important.log", "re-included\n")
			repository.write("source.txt", "source\n")
			repository.write("nested/ignored.log", "ignored in a nested directory\n")
			repository.write("nested/important.log", "re-included in a nested directory\n")
			repository.write("generated/drop.txt", "ignored generated file\n")
			repository.write("generated/keep.txt", "re-included generated file\n")
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
		Entry("enabled by default", false,
			[]string{"important.log", "source.txt", "nested/important.log", "generated/keep.txt"}),
		Entry("explicitly disabled", true, []string{
			"ignored.log", "important.log", "source.txt", "nested/ignored.log", "nested/important.log",
			"generated/drop.txt", "generated/keep.txt",
		}),
	)

	It("uses an existing partially staged set exactly", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("other.txt", "other tracked file\n")
		repository.git("add", "--", "other.txt")
		repository.git("commit", "--no-gpg-sign", "-m", "test: seed another tracked file")
		repository.write("tracked.txt", "initial\nstaged version\n")
		repository.git("add", "--", "tracked.txt")
		repository.append("tracked.txt", "unstaged version\n")
		repository.append("other.txt", "unrelated tracked edit\n")
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
		Expect(repository.git("show", "HEAD:other.txt")).To(Equal("other tracked file\n"))
		Expect(repository.read("other.txt")).To(Equal("other tracked file\nunrelated tracked edit\n"))
		Expect(repository.status()).To(ContainSubstring(" M tracked.txt"))
		Expect(repository.status()).To(ContainSubstring("?? unrelated.txt"))
		prompt := api.requestsFor(providerOpenAI)[0].Prompt
		Expect(prompt).To(ContainSubstring("staged version"))
		Expect(prompt).NotTo(ContainSubstring("unstaged version"))
		Expect(prompt).NotTo(ContainSubstring("unrelated.txt"))
		Expect(prompt).NotTo(ContainSubstring("other.txt"))
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

	DescribeTable("rejects intent-to-add entries without losing them",
		func(ctx SpecContext, withStagedContent bool) {
			repository := newRepository()
			if withStagedContent {
				repository.append("tracked.txt", "already staged\n")
				repository.git("add", "--", "tracked.txt")
			}
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
			Expect(repository.head()).To(Equal(repository.InitialHead))
			Expect(privateStagingFiles(repository)).To(BeEmpty())
		},
		Entry("with no staged content", false),
		Entry("alongside staged content", true),
	)

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
			Expect(privateStagingFiles(repository)).To(BeEmpty())
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
		externalIndex := repository.indexBytes()
		releaseProvider()
		result := resultFromSession(ctx, session, commandTimeout)

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("git index changed while preparing the commit"))
		Expect(repository.indexBytes()).To(Equal(externalIndex))
		Expect(repository.cachedDiff()).To(ContainSubstring("staged by another process"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(privateStagingFiles(repository)).To(BeEmpty())
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

var _ = Describe("Git staging edge cases", func() {
	It("keeps temporary staging private and removes it after a dry run", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "private change\n")
		repository.write("new.txt", "private new file\n")
		beforeIndex := repository.indexBytes()

		session, api, release := startCLIWithPausedProvider(ctx, repository, "--dry-run")
		privateFiles := privateStagingFiles(repository)
		Expect(privateFiles).To(HaveLen(1))
		if runtime.GOOS != "windows" {
			info, err := os.Stat(privateFiles[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		}
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.cachedDiff()).To(BeEmpty())
		_, err := repository.tryGit("commit", "-m", "must not consume temporary staging")
		Expect(err).To(HaveOccurred())
		Expect(repository.head()).To(Equal(repository.InitialHead))
		afterExternalGit := repository.indexBytes()
		Expect(api.requestsFor(providerOpenAI)[0].Prompt).To(ContainSubstring("+private change"))
		Expect(api.requestsFor(providerOpenAI)[0].Prompt).To(ContainSubstring("+private new file"))

		release()
		result := resultFromSession(ctx, session, commandTimeout)
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.indexBytes()).To(Equal(afterExternalGit))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(privateStagingFiles(repository)).To(BeEmpty())
	})

	DescribeTable("preserves a concurrent index metadata rewrite",
		func(ctx SpecContext, dryRun bool) {
			repository := newRepository()
			repository.git("update-index", "--index-version=2")
			repository.append("tracked.txt", "temporary change\n")
			beforeIndex := repository.indexBytes()
			var args []string
			if dryRun {
				args = append(args, "--dry-run")
			}
			session, _, release := startCLIWithPausedProvider(ctx, repository, args...)
			repository.git("update-index", "--index-version=4")
			rewrittenIndex := repository.indexBytes()
			Expect(rewrittenIndex).NotTo(Equal(beforeIndex))

			release()
			result := resultFromSession(ctx, session, commandTimeout)
			if dryRun {
				Expect(result.ExitCode).To(Equal(0), result.Output())
			} else {
				Expect(result.ExitCode).To(Equal(1))
				Expect(result.Output()).To(ContainSubstring("git index changed"))
			}
			Expect(repository.indexBytes()).To(Equal(rewrittenIndex))
			Expect(repository.head()).To(Equal(repository.InitialHead))
			Expect(privateStagingFiles(repository)).To(BeEmpty())
		},
		Entry("during a dry run", true),
		Entry("by rejecting a stale commit", false),
	)

	It("uses a linked worktree's index and honors its info exclude file", func(ctx SpecContext) {
		main := newRepository()
		linkedPath := filepath.Join(GinkgoT().TempDir(), "linked")
		main.git("worktree", "add", "-b", "linked", linkedPath)
		linked := &gitRepository{Path: linkedPath, GlobalConfig: main.GlobalConfig, InitialHead: main.InitialHead}
		main.write("main-only.txt", "staged only in the main worktree\n")
		main.git("add", "main-only.txt")
		mainIndex := main.indexBytes()
		linked.write("tracked.txt", "linked worktree change\n")
		linked.write("local-secret.env", "must stay out\n")
		excludePath := linked.gitPath("info/exclude")
		Expect(os.MkdirAll(filepath.Dir(excludePath), 0o755)).To(Succeed())
		Expect(os.WriteFile(excludePath, []byte("local-secret.env\n"), 0o600)).To(Succeed())
		linkedIndex := linked.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = main.GlobalConfig

		preview := runCLI(ctx, linkedPath, options, "--auto", "--dry-run", "--providers=openai")
		Expect(preview.ExitCode).To(Equal(0), preview.Output())
		Expect(linked.indexBytes()).To(Equal(linkedIndex))
		Expect(main.indexBytes()).To(Equal(mainIndex))
		Expect(privateStagingFiles(linked)).To(BeEmpty())

		result := runCLI(ctx, linkedPath, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(linked.git("show", "HEAD:tracked.txt")).To(Equal("linked worktree change\n"))
		Expect(linked.read("tracked.txt")).To(Equal("linked worktree change\n"))
		Expect(linked.git("show", "--format=", "--name-only", "HEAD")).To(Equal("tracked.txt\n"))
		Expect(main.head()).To(Equal(main.InitialHead))
		Expect(main.indexBytes()).To(Equal(mainIndex))
		for _, request := range api.requestsFor(providerOpenAI) {
			Expect(request.Prompt).NotTo(ContainSubstring("main-only.txt"))
			Expect(request.Prompt).NotTo(ContainSubstring("local-secret.env"))
		}
	})

	It("can preview and commit a packed nested branch", func(ctx SpecContext) {
		repository := newRepository()
		repository.git("switch", "-c", "feature/nested")
		repository.git("pack-refs", "--all", "--prune")
		repository.append("tracked.txt", "packed branch change\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		preview := runCLI(ctx, repository.Path, options, "--auto", "--dry-run", "--providers=openai")
		Expect(preview.ExitCode).To(Equal(0), preview.Output())
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(privateStagingFiles(repository)).To(BeEmpty())

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("branch", "--show-current")).To(Equal("feature/nested\n"))
		Expect(repository.git("show", "HEAD:tracked.txt")).To(ContainSubstring("packed branch change"))
		Expect(repository.status()).To(BeEmpty())
	})

	It("preserves staged filenames containing pathspec syntax and whitespace", func(ctx SpecContext) {
		if runtime.GOOS == "windows" {
			Skip("the fixture filename contains characters unsupported on Windows")
		}
		repository := newRepository()
		filename := ":(exclude)odd\nname\t-leading-π.txt"
		repository.write(filename, "unusual path\n")
		repository.git("--literal-pathspecs", "add", "--", filename)
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		preview := runCLI(ctx, repository.Path, options, "--auto", "--dry-run", "--providers=openai")
		Expect(preview.ExitCode).To(Equal(0), preview.Output())
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(api.requestsFor(providerOpenAI)[0].Prompt).To(ContainSubstring("+unusual path"))

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("show", "--format=", "--name-only", "-z", "HEAD")).To(Equal(filename + "\x00"))
		Expect(repository.status()).To(BeEmpty())
	})

	It("applies native clean filters and line ending normalization", func(ctx SpecContext) {
		if runtime.GOOS == "windows" {
			Skip("the fixture clean filter uses POSIX sed")
		}
		repository := newRepository()
		repository.git("config", "filter.scrub.clean", "sed s/raw/clean/g")
		repository.git("config", "filter.scrub.required", "true")
		repository.write(".gitattributes", "filtered.dat filter=scrub text eol=lf\n")
		repository.write("filtered.dat", "raw value\r\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("show", "HEAD:filtered.dat")).To(Equal("clean value\n"))
		prompt := api.requestsFor(providerOpenAI)[0].Prompt
		Expect(prompt).To(ContainSubstring("+clean value\n"))
		Expect(prompt).NotTo(ContainSubstring("+raw value"))
	})

	It("runs commit hooks in order and reports the rewritten message in the reflog", func(ctx SpecContext) {
		if runtime.GOOS == "windows" {
			Skip("the fixture hooks use POSIX shell scripts")
		}
		repository := newRepository()
		repository.append("tracked.txt", "committed through hooks\n")
		hooksPath := GinkgoT().TempDir()
		repository.git("config", "core.hooksPath", hooksPath)
		for name, contents := range map[string]string{
			"pre-commit":  "#!/bin/sh\nprintf 'pre-commit\\n' >> hook-order.log\n",
			"commit-msg":  "#!/bin/sh\nprintf 'commit-msg\\n' >> hook-order.log\nprintf 'rewritten by commit-msg\\n' > \"$1\"\n",
			"post-commit": "#!/bin/sh\nprintf 'post-commit\\n' >> hook-order.log\n",
		} {
			Expect(os.WriteFile(filepath.Join(hooksPath, name), []byte(contents), 0o700)).To(Succeed())
		}
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(result.Output()).To(ContainSubstring("rewritten by commit-msg"))
		Expect(repository.commitMessage()).To(Equal("rewritten by commit-msg"))
		Expect(repository.read("hook-order.log")).To(Equal("pre-commit\ncommit-msg\npost-commit\n"))
		Expect(repository.git("reflog", "-1", "--format=%H%x00%gs", "HEAD")).To(
			Equal(repository.head() + "\x00commit: rewritten by commit-msg\n"),
		)
		Expect(privateStagingFiles(repository)).To(BeEmpty())
	})

	DescribeTable("includes whitespace-only edits in the provider diff",
		func(ctx SpecContext, content string) {
			repository := newRepository()
			repository.git("config", "core.autocrlf", "false")
			repository.write("tracked.txt", content)
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--prompt={diff}")
			Expect(result.ExitCode).To(Equal(0), result.Output())
			Expect(api.requestsFor(providerOpenAI)[0].Prompt).To(ContainSubstring("+" + content))
			Expect(repository.git("show", "HEAD:tracked.txt")).To(Equal(content))
		},
		Entry("trailing whitespace", "initial \n"),
		Entry("carriage return at end of line", "initial\r\n"),
	)

	It("treats a missing index as staged deletions", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("tracked.txt", "working content after deleting the index\n")
		repository.write("untracked.txt", "untracked content\n")
		Expect(os.Remove(repository.gitPath("index"))).To(Succeed())
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("ls-tree", "-r", "--name-only", "HEAD")).To(BeEmpty())
		Expect(strings.Fields(repository.git("ls-files", "--others", "--exclude-standard"))).To(
			ConsistOf("tracked.txt", "untracked.txt"),
		)
		prompt := api.requestsFor(providerOpenAI)[0].Prompt
		Expect(prompt).To(ContainSubstring("-initial"))
		Expect(prompt).NotTo(ContainSubstring("working content after deleting the index"))
	})

	DescribeTable("matches complete path components in staging selectors",
		func(ctx SpecContext, arguments, expectedFiles []string, globalIgnore string) {
			repository := newRepository()
			for _, name := range []string{
				"api", "dialog.go", "log", "main.go", "nested/build/output.txt",
				"nested/rebuild/output.txt", "rapid.go", "rebuilder.c",
			} {
				repository.write(name, "fixture content\n")
			}
			if globalIgnore != "" {
				ignorePath := filepath.Join(GinkgoT().TempDir(), "ignore")
				Expect(os.WriteFile(ignorePath, []byte(globalIgnore), 0o600)).To(Succeed())
				repository.git("config", "core.excludesFile", ignorePath)
			}
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig
			arguments = append([]string{"--auto", "--providers=openai", "--prompt={files}"}, arguments...)

			result := runCLI(ctx, repository.Path, options, arguments...)
			Expect(result.ExitCode).To(Equal(0), result.Output())
			Expect(
				strings.Fields(repository.git("show", "--format=", "--name-only", "HEAD")),
			).To(ConsistOf(expectedFiles))
			Expect(strings.Split(api.requestsFor(providerOpenAI)[0].Prompt, ", ")).To(ConsistOf(expectedFiles))
			Expect(repository.cachedDiff()).To(BeEmpty())
		},
		Entry("exclude literals and directories", []string{"--exclude=log,go,build/"},
			[]string{"api", "dialog.go", "main.go", "nested/rebuild/output.txt", "rapid.go", "rebuilder.c"}, ""),
		Entry("include an exact name", []string{"--include-only=api"}, []string{"api"}, ""),
		Entry(
			"global ignore literal",
			[]string(nil),
			[]string{
				"api",
				"dialog.go",
				"log",
				"main.go",
				"nested/rebuild/output.txt",
				"rapid.go",
				"rebuilder.c",
			},
			"build\n",
		),
	)

	It("honors the default XDG ignore file while retaining tracked changes", func(ctx SpecContext) {
		repository := newRepository()
		xdg := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(xdg, "git"), 0o755)).To(Succeed())
		Expect(
			os.WriteFile(filepath.Join(xdg, "git", "ignore"), []byte("tracked.txt\nlocal-secret.env\n"), 0o600),
		).To(Succeed())
		repository.append("tracked.txt", "tracked changes remain eligible\n")
		repository.write("local-secret.env", "globally ignored\n")
		repository.write("visible.txt", "eligible new file\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig
		options.Environment = map[string]string{"XDG_CONFIG_HOME": xdg}

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(strings.Fields(repository.git("show", "--format=", "--name-only", "HEAD"))).To(
			ConsistOf("tracked.txt", "visible.txt"),
		)
		Expect(api.requestsFor(providerOpenAI)[0].Prompt).NotTo(ContainSubstring("local-secret.env"))
	})
})
