//go:build linux || darwin

package e2e_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/onsi/gomega/gbytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type interactiveResult struct {
	ExitCode int
}

func runInteractiveCLI(
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

	Eventually(output).WithContext(ctx).WithTimeout(commandTimeout).Should(gbytes.Say("Select Commit Message"))
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
	return interactiveResult{ExitCode: exitCode}
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
