//go:build linux || darwin

package e2e_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/creack/pty"
	"github.com/onsi/gomega/gbytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type interactiveResult struct {
	ExitCode int
	Output   string
}

func runInteractiveCLI(
	ctx context.Context,
	workingDirectory string,
	options runOptions,
	interact func(*os.File, *gbytes.Buffer),
	arguments ...string,
) interactiveResult {
	GinkgoHelper()
	return runTerminalCLI(ctx, workingDirectory, options, func(terminal *os.File, output *gbytes.Buffer) {
		Eventually(output).WithContext(ctx).WithTimeout(commandTimeout).Should(gbytes.Say("Select Commit Message"))
		interact(terminal, output)
	}, arguments...)
}

func runTerminalCLI(
	ctx context.Context,
	workingDirectory string,
	options runOptions,
	interact func(*os.File, *gbytes.Buffer),
	arguments ...string,
) interactiveResult {
	GinkgoHelper()

	command := exec.CommandContext(ctx, commitBinary, arguments...)
	command.Dir = workingDirectory
	command.Env = childEnvironment(options)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 40, Cols: 140})
	Expect(err).NotTo(HaveOccurred())

	output := gbytes.NewBuffer()
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, terminal)
		close(copyDone)
	}()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- command.Wait()
		close(waitDone)
	}()

	DeferCleanup(func() {
		_ = terminal.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		waitForCleanup("interactive command", waitDone)
		waitForCleanup("PTY output copy", copyDone)
	})

	interact(terminal, output)

	var waitError error
	Eventually(waitDone).WithContext(ctx).WithTimeout(commandTimeout).Should(Receive(&waitError))
	_ = terminal.Close()
	Eventually(copyDone).WithContext(ctx).WithTimeout(commandTimeout).Should(BeClosed())

	exitCode := 0
	if waitError != nil {
		var exitError *exec.ExitError
		Expect(errors.As(waitError, &exitError)).To(BeTrue(), "interactive command failed: %v", waitError)
		exitCode = exitError.ExitCode()
	}
	return interactiveResult{ExitCode: exitCode, Output: string(output.Contents())}
}

func waitForCleanup[T any](description string, done <-chan T) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		Fail("timed out waiting for " + description + " cleanup")
	}
}

var _ = Describe("Interactive terminal workflow", func() {
	DescribeTable("hands the foreground terminal to Git subprocesses",
		func(ctx SpecContext, filter bool) {
			repository := newRepository()
			script := "#!/bin/sh\nprintf '\\nGit input ready\\n' >/dev/tty\nIFS= read -r response </dev/tty\n"
			script += "test \"$response\" = allow || exit 1\n"
			if filter {
				filterPath := filepath.Join(GinkgoT().TempDir(), "clean-filter")
				Expect(os.WriteFile(filterPath, []byte(script+"cat\n"), 0o700)).To(Succeed())
				repository.git("config", "filter.terminal.clean", filterPath)
				repository.git("config", "filter.terminal.required", "true")
				repository.write(".gitattributes", "prompt.txt filter=terminal\n")
				repository.git("add", ".gitattributes")
				repository.git("commit", "-m", "test: configure terminal filter")
				repository.write("prompt.txt", "filter payload\n")
			} else {
				repository.append("tracked.txt", "terminal hook change\n")
				writeHook(repository, "pre-commit", script)
			}
			result := runTerminalCLI(ctx, repository.Path, repositoryOptions(repository),
				func(terminal *os.File, output *gbytes.Buffer) {
					Eventually(
						output,
					).WithContext(ctx).
						WithTimeout(commandTimeout).
						Should(gbytes.Say("Git input ready"))
					_, err := terminal.Write([]byte("allow\n"))
					Expect(err).NotTo(HaveOccurred())
				}, "--auto", "--providers=openai")
			Expect(result.ExitCode).To(Equal(0), result.Output)
			if filter {
				Expect(repository.git("show", "HEAD:prompt.txt")).To(Equal("filter payload\n"))
			} else {
				Expect(repository.git("show", "HEAD:tracked.txt")).To(ContainSubstring("terminal hook change"))
			}
			expectNoStagingArtifacts(repository)
		},
		Entry("a commit hook", false),
		Entry("a clean filter with buffered staging input", true),
	)

	It("contains a stopped Git process and releases the terminal", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "stopped hook change\n")
		beforeIndex := repository.indexBytes()
		writeHook(repository, "pre-commit", "#!/bin/sh\nprintf '\\nGit stop ready\\n' >/dev/tty\nsleep 30\n")
		result := runTerminalCLI(ctx, repository.Path, repositoryOptions(repository),
			func(terminal *os.File, output *gbytes.Buffer) {
				Eventually(output).WithContext(ctx).WithTimeout(commandTimeout).Should(gbytes.Say("Git stop ready"))
				_, err := terminal.Write([]byte{0x1a})
				Expect(err).NotTo(HaveOccurred())
			}, "--auto", "--providers=openai")
		Expect(result.ExitCode).To(Equal(1))
		Expect(result.Output).To(ContainSubstring("stopped while it owned the terminal"))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		expectNoStagingArtifacts(repository)
	})

	It("accepts the selected AI suggestion", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "interactive selection\n")
		api := newFakeAI()
		api.setReply(providerOpenAI, apiReply{Message: "feat(ui): accept selected suggestion"})
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runInteractiveCLI(
			ctx,
			repository.Path,
			options,
			func(terminal *os.File, _ *gbytes.Buffer) {
				_, err := terminal.Write([]byte("\r"))
				Expect(err).NotTo(HaveOccurred())
			},
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.commitMessage()).To(Equal("feat(ui): accept selected suggestion"))
	})

	It("cancels cleanly and restores temporary staging", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "interactive cancellation\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runInteractiveCLI(
			ctx,
			repository.Path,
			options,
			func(terminal *os.File, _ *gbytes.Buffer) {
				_, err := terminal.Write([]byte("q"))
				Expect(err).NotTo(HaveOccurred())
			},
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
		Expect(repository.status()).To(ContainSubstring(" M tracked.txt"))
	})

	It("commits a manually entered multiline message", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "manual message\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runInteractiveCLI(
			ctx,
			repository.Path,
			options,
			func(terminal *os.File, output *gbytes.Buffer) {
				_, err := terminal.Write([]byte("\x1b[B\r"))
				Expect(err).NotTo(HaveOccurred())
				Eventually(output).
					WithContext(ctx).
					WithTimeout(commandTimeout).
					Should(gbytes.Say("Write Your Commit Message"))
				_, err = terminal.Write([]byte("docs: enter a manual message\rwith a body\x04"))
				Expect(err).NotTo(HaveOccurred())
			},
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.commitMessage()).To(Equal("docs: enter a manual message\nwith a body"))
	})

	It("can enable dry-run from the terminal UI", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "UI dry run\n")
		beforeIndex := repository.indexBytes()
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runInteractiveCLI(
			ctx,
			repository.Path,
			options,
			func(terminal *os.File, _ *gbytes.Buffer) {
				_, err := terminal.Write([]byte("1\r"))
				Expect(err).NotTo(HaveOccurred())
			},
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.head()).To(Equal(repository.InitialHead))
		Expect(repository.indexBytes()).To(Equal(beforeIndex))
	})

	It("keeps tag choices mutually exclusive", func(ctx SpecContext) {
		repository := newRepository()
		repository.append("tracked.txt", "UI tag choice\n")
		api := newFakeAI()
		options := openAIOptions(api)
		options.GlobalConfig = repository.GlobalConfig

		result := runInteractiveCLI(
			ctx,
			repository.Path,
			options,
			func(terminal *os.File, output *gbytes.Buffer) {
				Expect(output.Clear()).To(Succeed())
				_, err := terminal.Write([]byte("3"))
				Expect(err).NotTo(HaveOccurred())
				Eventually(
					func() int { return len(output.Contents()) },
				).WithContext(ctx).WithTimeout(commandTimeout).Should(BeNumerically(">", 0))
				Expect(output.Clear()).To(Succeed())
				_, err = terminal.Write([]byte("5"))
				Expect(err).NotTo(HaveOccurred())
				Eventually(
					func() int { return len(output.Contents()) },
				).WithContext(ctx).WithTimeout(commandTimeout).Should(BeNumerically(">", 0))
				_, err = terminal.Write([]byte("\r"))
				Expect(err).NotTo(HaveOccurred())
			},
			"--providers=openai",
		)

		Expect(result.ExitCode).To(Equal(0))
		Expect(repository.git("tag", "--list")).To(Equal("v0.0.1\n"))
	})
})
