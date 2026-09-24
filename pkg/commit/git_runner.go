package commit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	minimumGitMajor     = 2
	minimumGitMinor     = 25
	gitCommandWaitDelay = time.Second
)

type gitCommandError struct {
	args       []string
	stderr     string
	cause      error
	dispatched bool
}

func (e *gitCommandError) Error() string {
	if e.stderr == "" {
		return fmt.Sprintf("git %s failed: %v", strings.Join(e.args, " "), e.cause)
	}
	return fmt.Sprintf("git %s failed: %v: %s", strings.Join(e.args, " "), e.cause, e.stderr)
}

func (e *gitCommandError) Unwrap() error {
	return e.cause
}

func (e *gitCommandError) ExitCode() (int, bool) {
	var exitError *exec.ExitError
	if !errors.As(e.cause, &exitError) {
		return 0, false
	}
	return exitError.ExitCode(), true
}

func (g *gitOperations) gitCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- gitPath comes from exec.LookPath("git"); Git receives argument vectors without a shell.
	cmd := exec.CommandContext(ctx, g.gitPath, args...)
	configureGitCommandCancellation(cmd)
	cmd.WaitDelay = gitCommandWaitDelay
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	if g.repoPath != "" {
		cmd.Dir = g.repoPath
	}
	return cmd
}

func (g *gitOperations) gitCommandContextWithIndex(
	ctx context.Context,
	indexPath string,
	args ...string,
) *exec.Cmd {
	cmd := g.gitCommandContext(ctx, args...)
	cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+indexPath)
	return cmd
}

func (g *gitOperations) runGit(
	ctx context.Context,
	indexPath string,
	stdin io.Reader,
	args ...string,
) ([]byte, error) {
	var cmd *exec.Cmd
	if indexPath == "" {
		cmd = g.gitCommandContext(ctx, args...)
	} else {
		cmd = g.gitCommandContextWithIndex(ctx, indexPath, args...)
	}
	cmd.Stdin = stdin

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := runConfiguredGitCommand(cmd, gitCommandMayUseTerminal(stdin, args)); err != nil {
		cause := err
		if contextErr := ctx.Err(); contextErr != nil {
			cause = errors.Join(contextErr, err)
		}
		return nil, &gitCommandError{
			args:       append([]string(nil), args...),
			stderr:     strings.TrimSpace(stderr.String()),
			cause:      cause,
			dispatched: cmd.Process != nil,
		}
	}
	return stdout.Bytes(), nil
}

func gitCommandMayUseTerminal(stdin io.Reader, args []string) bool {
	if input, ok := stdin.(*os.File); ok && input.Fd() == os.Stdin.Fd() {
		return true
	}
	switch gitCommandSubcommand(args) {
	case "add", "commit", "push", "tag":
		// Clean/process filters, signing, credential helpers, and hooks may
		// deliberately use /dev/tty even when standard input is not a TTY.
		return true
	default:
		return false
	}
}

// gitCommandSubcommand skips the Git-global options emitted before a
// subcommand by this package. Keep this list synchronized with any new global
// prefixes added to runGit call sites.
func gitCommandSubcommand(args []string) string {
	for len(args) != 0 {
		switch args[0] {
		case "-c":
			if len(args) < 2 {
				return ""
			}
			args = args[2:]
		case "--literal-pathspecs":
			args = args[1:]
		default:
			return args[0]
		}
	}
	return ""
}

var gitVersionPattern = regexp.MustCompile(`(?:^|\s)(\d+)\.(\d+)(?:\.(\d+))?`)

func validateGitVersion(ctx context.Context, executable string) error {
	cmd := exec.CommandContext(ctx, executable, "version")
	configureGitCommandCancellation(cmd)
	cmd.WaitDelay = gitCommandWaitDelay
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := runConfiguredGitCommand(cmd, false)
	output := stdout.Bytes()
	if err != nil {
		return fmt.Errorf(
			"failed to determine Git version: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}

	major, minor, err := parseGitVersion(string(output))
	if err != nil {
		return err
	}
	if major < minimumGitMajor || major == minimumGitMajor && minor < minimumGitMinor {
		return fmt.Errorf(
			"git %d.%d or newer is required; found %s",
			minimumGitMajor,
			minimumGitMinor,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func parseGitVersion(output string) (int, int, error) {
	matches := gitVersionPattern.FindStringSubmatch(output)
	if len(matches) == 0 {
		return 0, 0, fmt.Errorf("failed to parse Git version from %q", strings.TrimSpace(output))
	}
	major, majorErr := strconv.Atoi(matches[1])
	minor, minorErr := strconv.Atoi(matches[2])
	if majorErr != nil || minorErr != nil {
		return 0, 0, fmt.Errorf("failed to parse Git version from %q", strings.TrimSpace(output))
	}
	return major, minor, nil
}
