package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Repository state isolation", func() {
	for _, linked := range []bool{false, true} {
		location := "main worktree"
		if linked {
			location = "linked worktree"
		}
		DescribeTable("rejects operation markers only in the "+location,
			func(ctx SpecContext, marker, state string, directory bool) {
				main := newRepository()
				worktree := newLinkedRepository(main, "linked-state")
				selected, other := main, worktree
				if linked {
					selected, other = worktree, main
				}
				selected.append("tracked.txt", "selected change\n")
				other.append("tracked.txt", "independent change\n")
				markerPath := selected.gitPath(marker)
				if directory {
					Expect(os.MkdirAll(markerPath, 0o700)).To(Succeed())
				} else {
					Expect(os.WriteFile(markerPath, []byte(selected.head()+"\n"), 0o600)).To(Succeed())
				}
				beforeIndex := selected.indexBytes()
				options := repositoryOptions(selected)
				rejected := runCLI(ctx, selected.Path, options, "--auto", "--providers=openai")
				Expect(rejected.ExitCode).To(Equal(1))
				Expect(rejected.Output()).To(ContainSubstring(state))
				Expect(options.API.requestsFor(providerOpenAI)).To(BeEmpty())
				Expect(selected.indexBytes()).To(Equal(beforeIndex))
				Expect(selected.head()).To(Equal(selected.InitialHead))

				accepted := runCLI(ctx, other.Path, repositoryOptions(other), "--auto", "--providers=openai")
				Expect(accepted.ExitCode).To(Equal(0), accepted.Output())
				Expect(other.git("show", "HEAD:tracked.txt")).To(ContainSubstring("independent change"))
				Expect(selected.head()).To(Equal(selected.InitialHead))
			},
			Entry("merge", "MERGE_HEAD", "merging", false),
			Entry("rebase merge", "rebase-merge", "rebasing", true),
			Entry("rebase apply", "rebase-apply", "rebasing", true),
			Entry("cherry-pick", "CHERRY_PICK_HEAD", "cherry-picking", false),
			Entry("revert", "REVERT_HEAD", "reverting", false),
			Entry("bisect", "BISECT_LOG", "bisecting", false),
		)
	}

	DescribeTable("preserves conflicts with unusual filenames and rejects an unfinished merge",
		func(ctx SpecContext, linked bool) {
			repository := newRepository()
			filename := "conflict file.txt"
			if runtime.GOOS != "windows" {
				filename = "conflict\nname\t-leading.txt"
			}
			repository.write(filename, "base\n")
			repository.git("add", "--", filename)
			repository.git("commit", "-m", "test: conflict base")
			repository.git("switch", "-c", "incoming")
			repository.write(filename, "incoming\n")
			repository.git("add", "--", filename)
			repository.git("commit", "-m", "test: incoming")
			repository.git("switch", "master")
			target := repository
			if linked {
				target = newLinkedRepository(repository, "conflict-linked")
			}
			target.write(filename, "ours\n")
			target.git("add", "--", filename)
			target.git("commit", "-m", "test: ours")
			expectGitConflict(target, "merge", "incoming")
			head := target.head()
			beforeIndex := target.indexBytes()
			conflicts := target.git("ls-files", "--unmerged", "-z")
			Expect(conflicts).To(ContainSubstring("\t" + filename + "\x00"))
			options := repositoryOptions(target)
			result := runCLI(ctx, target.Path, options, "--auto", "--providers=openai")
			Expect(result.ExitCode).To(Equal(1))
			Expect(options.API.requestsFor(providerOpenAI)).To(BeEmpty())
			Expect(target.indexBytes()).To(Equal(beforeIndex))
			Expect(target.git("ls-files", "--unmerged", "-z")).To(Equal(conflicts))
			Expect(target.head()).To(Equal(head))

			target.write(filename, "resolved\n")
			target.git("add", "--", filename)
			Expect(target.git("ls-files", "--unmerged", "-z")).To(BeEmpty())
			result = runCLI(ctx, target.Path, options, "--auto", "--providers=openai")
			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring("merging"))
			Expect(options.API.requestsFor(providerOpenAI)).To(BeEmpty())
			if linked {
				repository.append("tracked.txt", "unaffected main worktree\n")
				result = runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
				Expect(result.ExitCode).To(Equal(0), result.Output())
			}
		},
		Entry("in the main worktree", false),
		Entry("in a linked worktree", true),
	)

	It("rejects reftable repositories before calling AI", func(ctx SpecContext) {
		repository := &gitRepository{Path: GinkgoT().TempDir(), GlobalConfig: os.DevNull}
		output, err := repository.tryGit("init", "-q", "--ref-format=reftable")
		if err != nil {
			Skip("installed Git cannot create the reftable fixture: " + output)
		}
		options := repositoryOptions(repository)
		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring("reftable repositories are not supported"))
		Expect(options.API.requestsFor(providerOpenAI)).To(BeEmpty())
	})

	It("writes an audit reflog even when the repository disables reflogs", func(ctx SpecContext) {
		repository := newRepository()
		repository.git("config", "core.logAllRefUpdates", "false")
		Expect(os.Remove(repository.gitPath("logs/HEAD"))).To(Succeed())
		repository.append("tracked.txt", "audited change\n")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("reflog", "--format=%H", "HEAD")).To(Equal(repository.head() + "\n"))
		Expect(repository.git("config", "--type=bool", "core.logAllRefUpdates")).To(Equal("false\n"))
	})

	It("keeps an unborn branch's temporary index private until the first commit", func(ctx SpecContext) {
		repository := newUnbornRepository()
		repository.write("first.txt", "first commit\n")
		indexPath := repository.gitPath("index")
		Expect(indexPath).NotTo(BeAnExistingFile())
		session, api, release := startCLIWithPausedProvider(ctx, repository)
		Expect(indexPath).NotTo(BeAnExistingFile())
		files := privateStagingFiles(repository)
		Expect(files).To(HaveLen(1))
		if runtime.GOOS != "windows" {
			info, err := os.Stat(files[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		}
		Expect(api.requestsFor(providerOpenAI)[0].Prompt).To(ContainSubstring("+first commit"))
		release()
		result := resultFromSession(ctx, session, commandTimeout)
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.commitCount()).To(Equal("1"))
		Expect(repository.cachedDiff()).To(BeEmpty())
		expectNoStagingArtifacts(repository)
	})
})

func newLinkedRepository(main *gitRepository, branch string) *gitRepository {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "linked")
	main.git("worktree", "add", "-b", branch, path)
	return &gitRepository{Path: path, GlobalConfig: main.GlobalConfig,
		InitialHead: strings.TrimSpace(main.git("rev-parse", "HEAD"))}
}
