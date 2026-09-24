package e2e_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onsi/gomega/gexec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Commit hooks and recovery", func() {
	BeforeEach(func() {
		if runtime.GOOS == "windows" {
			Skip("the hook fixtures use POSIX shell scripts")
		}
	})

	It("discards staging added by a rejecting hook", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "rejected change\n")
		beforeIndex := repository.indexBytes()
		writeHook(
			repository,
			"pre-commit",
			"#!/bin/sh\nprintf 'hook-only\\n' > hook-only.txt\ngit add -- hook-only.txt\nexit 37\n",
		)
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).NotTo(ContainSubstring("requires recovery"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.cachedDiff()).To(BeEmpty())
		Expect(repository.read("hook-only.txt")).To(Equal("hook-only\n"))
		expectNoStagingArtifacts(repository)
	})

	It("preserves new staging from a post-commit hook without adding it to the commit", func(ctx SpecContext) {
		repository := newRepository()
		repository.write("later.txt", "old content\n")
		repository.git("add", "later.txt")
		repository.git("commit", "-m", "test: seed post-commit file")
		repository.append("tracked.txt", "selected change\n")
		repository.write("later.txt", "new content\n")
		writeHook(repository, "post-commit", "#!/bin/sh\ngit add -- later.txt\n")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository),
			"--auto", "--providers=openai", "--include-only=tracked.txt")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("show", "HEAD:later.txt")).To(Equal("old content\n"))
		Expect(repository.git("show", ":later.txt")).To(Equal("new content\n"))
		Expect(repository.git("diff", "--cached", "--name-only")).To(Equal("later.txt\n"))
		expectNoStagingArtifacts(repository)
	})

	It("reports the complete message rewritten by a commit-msg hook", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "rewritten message\n")
		writeHook(repository, "commit-msg", "#!/bin/sh\nprintf 'rewritten subject\\n\\nrewritten body\\n' > \"$1\"\n")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(result.Output()).To(ContainSubstring("rewritten subject\\n\\nrewritten body\\n"))
		Expect(repository.git("log", "-1", "--format=%B")).To(Equal("rewritten subject\n\nrewritten body\n\n"))
		Expect(repository.git("reflog", "-1", "--format=%gs")).To(Equal("commit: rewritten subject\n"))
	})

	It("blocks an external index writer while the commit hook runs", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "selected change\n")
		session, release := startCLIAtHook(ctx, repository, "pre-commit")
		repository.write("external.txt", "external writer\n")
		output, err := repository.tryGit("add", "--", "external.txt")
		Expect(err).To(HaveOccurred())
		Expect(output).To(ContainSubstring("index.lock"))
		release()
		result := resultFromSession(ctx, session, commandTimeout)
		Expect(result.ExitCode).To(Equal(0), result.Output())
		Expect(repository.git("show", "HEAD:tracked.txt")).To(ContainSubstring("selected change"))
		Expect(repository.cachedDiff()).To(BeEmpty())
		Expect(repository.status()).To(ContainSubstring("?? external.txt"))
		expectNoStagingArtifacts(repository)
	})

	It("retains recovery artifacts when HEAD changes during the commit", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "selected change\n")
		beforeIndex := repository.indexBytes()
		session, release := startCLIAtHook(ctx, repository, "pre-commit")
		externalHead := strings.TrimSpace(repository.git("commit-tree", "HEAD^{tree}",
			"-p", repository.InitialHead, "-m", "external ref-only commit"))
		repository.git("update-ref", "HEAD", externalHead, repository.InitialHead)
		release()
		result := resultFromSession(ctx, session, commandTimeout)
		Expect(result.ExitCode).To(Equal(1))
		expectCommitRecovery(repository, result)
		repository.git("merge-base", "--is-ancestor", externalHead, "HEAD")
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
	})

	It("retains recovery artifacts when a post-commit hook replaces HEAD", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "selected change\n")
		beforeIndex := repository.indexBytes()
		writeHook(repository, "post-commit", `#!/bin/sh
set -eu
parent=$(git rev-parse HEAD^)
replacement=$(printf 'replacement sibling\n' | git commit-tree "$parent^{tree}" -p "$parent")
current=$(git rev-parse HEAD)
git update-ref "$(git symbolic-ref HEAD)" "$replacement" "$current"
`)
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(1))
		expectCommitRecovery(repository, result)
		Expect(repository.git("show", "HEAD:tracked.txt")).To(Equal("initial\n"))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
	})

	It("retains both index locks when a hook leaves the private index locked", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "recovery change\n")
		beforeIndex := repository.indexBytes()
		writeHook(repository, "post-commit", "#!/bin/sh\n: > \"$GIT_INDEX_FILE.lock\"\n")
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(1))
		expectCommitRecovery(repository, result)
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.head()).NotTo(Equal(repository.InitialHead))
		privateFiles := privateStagingFiles(repository)
		Expect(privateFiles).To(HaveLen(2))
		for _, path := range privateFiles {
			if !strings.HasSuffix(path, ".lock") {
				Expect(path + ".lock").To(BeAnExistingFile())
			}
		}
	})

	It("reconciles a created commit after cancellation and stops its hook", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "canceled after creation\n")
		options := repositoryOptions(repository)
		marker := filepath.Join(GinkgoT().TempDir(), "hook-entered")
		options.Environment = map[string]string{"E2E_HOOK_ENTERED": marker}
		writeHook(repository, "post-commit", `#!/bin/sh
: > "$E2E_HOOK_ENTERED"
sleep 2
printf 'late mutation\n' > late-hook.txt
git add -- late-hook.txt
git update-ref "$(git symbolic-ref HEAD)" HEAD^
`)
		session := startCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
		Eventually(marker).WithContext(ctx).WithTimeout(commandTimeout).Should(BeAnExistingFile())
		createdHead := repository.head()
		Expect(createdHead).NotTo(Equal(repository.InitialHead))
		session.Signal(os.Interrupt)
		result := resultFromSession(ctx, session, commandTimeout)
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring(createdHead))
		Expect(result.Output()).To(ContainSubstring("context canceled"))
		Expect(repository.head()).To(Equal(createdHead))
		Expect(repository.cachedDiff()).To(BeEmpty())
		expectNoStagingArtifacts(repository)
		Consistently(filepath.Join(repository.Path, "late-hook.txt")).
			WithContext(ctx).WithTimeout(2200 * time.Millisecond).ShouldNot(BeAnExistingFile())
		Expect(repository.head()).To(Equal(createdHead))
	})

	It("stops redirected background hooks before they can mutate the repository", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "background hook change\n")
		writeHook(repository, "post-commit", `#!/bin/sh
(
	sleep 2
	printf 'late background mutation\n' > late-hook.txt
	git add -- late-hook.txt
	git update-ref "$(git symbolic-ref HEAD)" HEAD^
) </dev/null >/dev/null 2>&1 &
`)
		result := runCLI(ctx, repository.Path, repositoryOptions(repository), "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(0), result.Output())
		createdHead := repository.head()
		Consistently(filepath.Join(repository.Path, "late-hook.txt")).
			WithContext(ctx).WithTimeout(2200 * time.Millisecond).ShouldNot(BeAnExistingFile())
		Expect(repository.head()).To(Equal(createdHead))
		Expect(repository.cachedDiff()).To(BeEmpty())
		expectNoStagingArtifacts(repository)
	})
})

func startCLIAtHook(ctx SpecContext, repository *gitRepository, hook string) (*gexec.Session, func()) {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	entered, released := filepath.Join(directory, "entered"), filepath.Join(directory, "released")
	options := repositoryOptions(repository)
	options.Environment = map[string]string{"E2E_HOOK_ENTERED": entered, "E2E_HOOK_RELEASE": released}
	writeHook(repository, hook, `#!/bin/sh
: > "$E2E_HOOK_ENTERED"
while test ! -e "$E2E_HOOK_RELEASE"; do sleep 0.02; done
`)
	release := func() { Expect(os.WriteFile(released, nil, 0o600)).To(Succeed()) }
	session := startCLI(ctx, repository.Path, options, "--auto", "--providers=openai")
	DeferCleanup(release)
	Eventually(entered).WithContext(ctx).WithTimeout(commandTimeout).Should(BeAnExistingFile())
	return session, release
}

func expectCommitRecovery(repository *gitRepository, result commandResult) {
	GinkgoHelper()
	Expect(result.Output()).To(ContainSubstring("requires recovery"))
	Expect(repository.gitPath("index") + ".lock").To(BeAnExistingFile())
	files := privateStagingFiles(repository)
	Expect(files).NotTo(BeEmpty())
	for _, path := range files {
		Expect(path).To(BeAnExistingFile())
	}
}
