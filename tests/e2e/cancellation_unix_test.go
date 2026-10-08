//go:build linux || darwin

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Post-commit cancellation", func() {
	DescribeTable("reports an indeterminate remote outcome after push dispatch",
		func(ctx SpecContext, tag bool) {
			repository := newPushRepository()
			remote := newBareRemote(repository.GlobalConfig)
			repository.git("remote", "add", "origin", remote)
			marker := filepath.Join(GinkgoT().TempDir(), "push-entered")
			options := repositoryOptions(repository)
			options.Environment = map[string]string{"E2E_PUSH_ENTERED": marker}
			if tag {
				options.Environment["E2E_PUSH_TAG"] = "1"
			}
			hook := `#!/bin/sh
while read -r old new ref; do
	if test "$E2E_PUSH_TAG" != 1 || test "$ref" = refs/tags/v0.0.1; then
		: > "$E2E_PUSH_ENTERED"
		sleep 30
	fi
done
`
			Expect(os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o700)).To(Succeed())
			args := []string{"--auto", "--providers=openai", "--push"}
			if tag {
				args = append(args, "--tag=patch")
			}
			session := startCLI(ctx, repository.Path, options, args...)
			Eventually(marker).WithContext(ctx).WithTimeout(commandTimeout).Should(BeAnExistingFile())
			session.Signal(os.Interrupt)
			result := resultFromSession(ctx, session, commandTimeout)
			Expect(result.ExitCode).To(Equal(1))
			operation := "branch push"
			if tag {
				operation = "tag push"
			}
			Expect(result.Output()).To(ContainSubstring(operation + " did not complete cleanly after dispatch"))
			Expect(result.Output()).To(ContainSubstring("remote may have updated"))
			Expect(result.Output()).To(ContainSubstring("context canceled"))
			Expect(repository.commitCount()).To(Equal("2"))
			expectNoStagingArtifacts(repository)
		},
		Entry("branch push", false),
		Entry("tag push", true),
	)

	DescribeTable("reconciles tag creation interrupted after dispatch",
		func(ctx SpecContext, created bool) {
			repository := newRepository()
			repository.append("tracked.txt", "tagged change\n")
			repository.git("config", "tag.gpgsign", "false")
			options := repositoryOptions(repository)
			marker := filepath.Join(GinkgoT().TempDir(), "tag-entered")
			options.Environment = map[string]string{"E2E_TAG_ENTERED": marker}
			if created {
				options.Environment["E2E_TAG_AFTER"] = "1"
			}
			installGitWrapper(&options, `#!/bin/sh
if test "$1" = tag && test "$2" = -a; then
	if test "$E2E_TAG_AFTER" = 1; then "$E2E_REAL_GIT" "$@" || exit $?; fi
	: > "$E2E_TAG_ENTERED"
	sleep 30
fi
exec "$E2E_REAL_GIT" "$@"
`)
			session := startCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--tag=patch")
			Eventually(marker).WithContext(ctx).WithTimeout(commandTimeout).Should(BeAnExistingFile())
			session.Signal(os.Interrupt)
			result := resultFromSession(ctx, session, commandTimeout)
			Expect(result.ExitCode).To(Equal(1))
			Expect(result.Output()).To(ContainSubstring("context canceled"))
			Expect(result.Output()).To(ContainSubstring("retry"))
			if created {
				objectID := strings.TrimSpace(repository.git("rev-parse", "refs/tags/v0.0.1"))
				Expect(result.Output()).To(ContainSubstring(objectID))
				Expect(result.Output()).To(ContainSubstring("must be treated as created"))
			} else {
				Expect(result.Output()).To(ContainSubstring("indeterminate"))
				Expect(repository.git("tag", "--list")).To(BeEmpty())
			}
		},
		Entry("after the tag was created", true),
		Entry("before the tag was created", false),
	)

	It("refuses to push a tag removed after creation", func(ctx SpecContext) {
		repository := newPushRepository()
		remote := newBareRemote(repository.GlobalConfig)
		repository.git("remote", "add", "origin", remote)
		options := repositoryOptions(repository)
		installGitWrapper(&options, `#!/bin/sh
if test "$1" = tag && test "$2" = -a; then
	"$E2E_REAL_GIT" "$@" || exit $?
	exec "$E2E_REAL_GIT" update-ref -d refs/tags/v0.0.1
fi
exec "$E2E_REAL_GIT" "$@"
`)
		result := runCLI(ctx, repository.Path, options, "--auto", "--providers=openai", "--push", "--tag=patch")
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output()).To(ContainSubstring(`tag \"v0.0.1\" does not exist`))
		Expect(repository.git("ls-remote", remote)).To(Equal(repository.head() + "\trefs/heads/topic\n"))
	})
})

// Inject failures at the executable boundary without accessing package internals.
func installGitWrapper(options *runOptions, script string) {
	GinkgoHelper()
	realGit, err := exec.LookPath("git")
	Expect(err).NotTo(HaveOccurred())
	directory := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0o700)).To(Succeed())
	if options.Environment == nil {
		options.Environment = map[string]string{}
	}
	options.Environment["E2E_REAL_GIT"] = realGit
	options.Environment["PATH"] = directory + string(os.PathListSeparator) + os.Getenv("PATH")
}
