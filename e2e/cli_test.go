package e2e_test

import (
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type operationSetup func(*gitRepository)

func commitTrackedChange(repository *gitRepository, contents, message string) string {
	GinkgoHelper()
	repository.write("tracked.txt", contents)
	repository.git("add", "--", "tracked.txt")
	repository.git("commit", "--no-gpg-sign", "-m", message)
	return repository.head()
}

func createConflictingBranches(repository *gitRepository) string {
	GinkgoHelper()
	repository.git("switch", "-c", "operation")
	operationCommit := commitTrackedChange(repository, "operation branch\n", "test: operation branch")
	repository.git("switch", "main")
	commitTrackedChange(repository, "main branch\n", "test: main branch")
	return operationCommit
}

func expectGitConflict(repository *gitRepository, arguments ...string) {
	GinkgoHelper()
	output, err := repository.tryGit(arguments...)
	Expect(err).To(HaveOccurred(), "git %v unexpectedly succeeded:\n%s", arguments, output)
	Expect(output).To(ContainSubstring("CONFLICT"))
}

func prepareMergeConflict(repository *gitRepository) {
	GinkgoHelper()
	createConflictingBranches(repository)
	expectGitConflict(repository, "merge", "operation")
}

func prepareRebaseConflict(repository *gitRepository) {
	GinkgoHelper()
	createConflictingBranches(repository)
	repository.git("switch", "operation")
	expectGitConflict(repository, "rebase", "main")
}

func prepareCherryPickConflict(repository *gitRepository) {
	GinkgoHelper()
	operationCommit := createConflictingBranches(repository)
	expectGitConflict(repository, "cherry-pick", operationCommit)
}

func prepareRevertConflict(repository *gitRepository) {
	GinkgoHelper()
	commitToRevert := commitTrackedChange(repository, "change to revert\n", "test: change to revert")
	commitTrackedChange(repository, "later competing change\n", "test: competing change")
	expectGitConflict(repository, "revert", "--no-edit", commitToRevert)
}

func prepareBisect(repository *gitRepository) {
	GinkgoHelper()
	for _, contents := range []string{"change one\n", "change two\n", "change three\n", "change four\n"} {
		commitTrackedChange(repository, contents, "test: add bisect history")
	}
	repository.git("bisect", "start")
	repository.git("bisect", "bad", repository.head())
	repository.git("bisect", "good", repository.InitialHead)
	repository.append("tracked.txt", "pending bisect change\n")
}

var _ = Describe("CLI contract", func() {
	It("prints help without credentials or a repository", func(ctx SpecContext) {
		result := runCLI(ctx, GinkgoT().TempDir(), runOptions{}, "--help")

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Stdout).To(ContainSubstring("Commit helper tool"))
		Expect(result.Stdout).To(ContainSubstring("Usage:\n  commit [flags]"))
		Expect(result.Stdout).To(ContainSubstring("version     Version information"))
		Expect(result.Stdout).To(ContainSubstring("--dry-run"))
		Expect(result.Stderr).To(BeEmpty())
	})

	It("prints build and platform information", func(ctx SpecContext) {
		result := runCLI(ctx, GinkgoT().TempDir(), runOptions{}, "version")

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Stdout).To(MatchRegexp(`(?m)^Version:\s+\S+`))
		Expect(result.Stdout).To(MatchRegexp(`(?m)^Go:\s+go\d+`))
		Expect(result.Stdout).To(ContainSubstring("OS/Arch: " + runtime.GOOS + "/" + runtime.GOARCH))
		Expect(result.Stderr).To(BeEmpty())
	})

	It("rejects unknown flags", func(ctx SpecContext) {
		result := runCLI(ctx, GinkgoT().TempDir(), runOptions{}, "--not-a-real-flag")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Stderr).To(ContainSubstring("unknown flag: --not-a-real-flag"))
	})

	DescribeTable("validates public settings before performing work",
		func(ctx SpecContext, argument, expectedError string) {
			repository := newRepository()
			api := newFakeAI()

			result := runCLI(
				ctx,
				repository.Path,
				openAIOptions(api),
				"--auto",
				"--providers=openai",
				argument,
			)

			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring(expectedError))
			Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		},
		Entry("a zero timeout", "--timeout=0s", "timeout must be greater than zero"),
		Entry("a negative maximum diff size", "--max-diff-size-bytes=-1", "max diff size bytes cannot be negative"),
		Entry("an unknown tag increment", "--tag=calendar", "invalid tag increment type: calendar"),
		Entry("an unknown Jira position", "--jira-task-position=middle", "invalid jira task position: middle"),
		Entry("an unknown Jira style", "--jira-task-style=curly", "invalid jira task style: curly"),
	)

	It("reads flags from COMMIT environment variables", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "environment change\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.Environment = map[string]string{
			"COMMIT_AUTO":      "true",
			"COMMIT_DRY_RUN":   "true",
			"COMMIT_PROVIDERS": "openai",
			"COMMIT_PROMPT":    "env prompt: {branch} | {files}",
		}
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("Dry run enabled"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		requests := api.requestsFor(providerOpenAI)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Prompt).To(Equal("env prompt: main | tracked.txt"))
	})

	It("gives command-line flags precedence over the environment", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "committed change\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.Environment = map[string]string{
			"COMMIT_AUTO":      "false",
			"COMMIT_DRY_RUN":   "true",
			"COMMIT_PROVIDERS": "missing",
		}
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(
			ctx,
			repository.Path,
			options,
			"--auto=true",
			"--dry-run=false",
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.head()).NotTo(Equal(repository.InitialHead))
		Expect(repository.commitMessage()).To(Equal(defaultAIResponse))
	})

	It("honors debug log filtering", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "debug me\n")
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
			"--log-level=debug",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Stdout).To(ContainSubstring("Preparing staged files"))
		Expect(result.Stdout).To(ContainSubstring("Requesting commit messages"))
	})
})

var _ = Describe("Repository preconditions", func() {
	It("requires at least one API credential", func(ctx SpecContext) {
		repository := newRepository()
		result := runCLI(ctx, repository.Path, runOptions{GlobalConfig: repository.GlobalConfig}, "--auto")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("no api keys found in environment"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	It("rejects execution outside a Git worktree", func(ctx SpecContext) {
		api := newFakeAI()
		result := runCLI(ctx, GinkgoT().TempDir(), openAIOptions(api), "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("failed to open git repository"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
	})

	It("returns successfully without calling AI when there are no changes", func(ctx SpecContext) {
		repository := newRepository()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(0))
		Expect(result.Output()).To(ContainSubstring("No files to commit"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.head()).To(Equal(repository.InitialHead))
	})

	DescribeTable("rejects an in-progress repository operation",
		func(ctx SpecContext, state string, prepare operationSetup) {
			repository := newRepository()
			prepare(repository)
			headBeforeRun := repository.head()
			api := newFakeAI()
			options := openAIOptions(api)
			options.GlobalConfig = repository.GlobalConfig

			result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring("repository is in " + state + " state"))
			Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
			Expect(repository.head()).To(Equal(headBeforeRun))
		},
		Entry("merge", "merging", operationSetup(prepareMergeConflict)),
		Entry("rebase", "rebasing", operationSetup(prepareRebaseConflict)),
		Entry("cherry-pick", "cherry-picking", operationSetup(prepareCherryPickConflict)),
		Entry("revert", "reverting", operationSetup(prepareRevertConflict)),
		Entry("bisect", "bisecting", operationSetup(prepareBisect)),
	)

	It("rejects externally routed Git indexes", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "should remain untouched\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig
		options.Environment = map[string]string{
			"GIT_INDEX_FILE": filepath.Join(GinkgoT().TempDir(), "alternate-index"),
		}

		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")

		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("GIT_INDEX_FILE is not supported"))
		Expect(api.requestsFor(providerOpenAI)).To(BeEmpty())
		Expect(repository.status()).To(ContainSubstring(" M tracked.txt"))
	})
})
