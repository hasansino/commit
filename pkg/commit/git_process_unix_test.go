//go:build linux || darwin

package commit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

const (
	gitTerminalHelperEnv = "COMMIT_TEST_GIT_TERMINAL_HELPER"
	gitTerminalScriptEnv = "COMMIT_TEST_GIT_TERMINAL_SCRIPT"
	gitTerminalStopEnv   = "COMMIT_TEST_GIT_TERMINAL_STOP_HELPER"
	gitTerminalMarkerEnv = "COMMIT_TEST_GIT_TERMINAL_MARKER"
	gitFilterHelperEnv   = "COMMIT_TEST_GIT_FILTER_TERMINAL_HELPER"
	gitFilterRepoEnv     = "COMMIT_TEST_GIT_FILTER_TERMINAL_REPO"
)

func TestGitCommandTerminalForegroundHandoff(t *testing.T) {
	if os.Getenv(gitTerminalHelperEnv) == "1" {
		runGitTerminalHelper(t)
		return
	}

	scriptPath := writeGitTerminalHelperScript(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitCommandTerminalForegroundHandoff$")
	cmd.Env = append(
		os.Environ(),
		gitTerminalHelperEnv+"=1",
		gitTerminalScriptEnv+"="+scriptPath,
		"TERM=dumb",
		"NO_COLOR=1",
	)
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}

	ready, output := captureTerminalOutput(terminal, "terminal helper ready")
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		_ = terminal.Close()
		_ = cmd.Wait()
		t.Fatalf("terminal helper did not request input: %q", <-output)
	}
	if _, err := terminal.Write([]byte("signed\n")); err != nil {
		t.Fatal(err)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		_ = terminal.Close()
		contents := <-output
		if err != nil {
			t.Fatalf("terminal helper failed: %v\n%s", err, contents)
		}
		if !strings.Contains(string(contents), "terminal handoff succeeded") {
			t.Fatalf("terminal helper output = %q", contents)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = terminal.Close()
		<-waited
		contents := <-output
		t.Fatalf("terminal helper hung, likely stopped on a background TTY read: %q", contents)
	}
}

func TestGitAddFilterTerminalForegroundHandoff(t *testing.T) {
	if os.Getenv(gitFilterHelperEnv) == "1" {
		runGitFilterTerminalHelper(t)
		return
	}

	repoPath := newStagingIntegrationRepo(t)
	filterPath := filepath.Join(repoPath, ".git", "terminal-clean-filter")
	filter := `#!/bin/sh
printf '\nstaging filter ready\n' >/dev/tty
IFS= read -r response </dev/tty
test "$response" = allow || exit 1
cat
`
	if err := os.WriteFile(filterPath, []byte(filter), 0o700); err != nil {
		t.Fatal(err)
	}
	runStagingIntegrationGit(t, repoPath, nil, "config", "filter.terminal.clean", filterPath)
	runStagingIntegrationGit(t, repoPath, nil, "config", "filter.terminal.required", "true")
	writeStagingIntegrationFile(t, repoPath, ".gitattributes", "prompt.txt filter=terminal\n")
	runStagingIntegrationGit(t, repoPath, nil, "add", "--", ".gitattributes")
	runStagingIntegrationGit(t, repoPath, nil, "commit", "-q", "-m", "configure terminal filter")
	writeStagingIntegrationFile(t, repoPath, "prompt.txt", "filter payload\n")

	cmd := exec.Command(os.Args[0], "-test.run=^TestGitAddFilterTerminalForegroundHandoff$")
	cmd.Env = append(
		os.Environ(),
		gitFilterHelperEnv+"=1",
		gitFilterRepoEnv+"="+repoPath,
		"TERM=dumb",
		"NO_COLOR=1",
	)
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	ready, output := captureTerminalOutput(terminal, "staging filter ready")
	select {
	case <-ready:
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		_ = terminal.Close()
		_ = cmd.Wait()
		t.Fatalf("staging filter did not request terminal input: %q", <-output)
	}
	if _, err := terminal.Write([]byte("allow\n")); err != nil {
		t.Fatal(err)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		_ = terminal.Close()
		contents := <-output
		if err != nil {
			t.Fatalf("staging filter helper failed: %v\n%s", err, contents)
		}
		if !strings.Contains(string(contents), "staging filter handoff succeeded") {
			t.Fatalf("staging filter helper output = %q", contents)
		}
	case <-time.After(12 * time.Second):
		_ = cmd.Process.Kill()
		_ = terminal.Close()
		<-waited
		t.Fatalf("staging filter retained or could not read the terminal: %q", <-output)
	}
}

func TestGitCommandTerminalStopDoesNotHang(t *testing.T) {
	if os.Getenv(gitTerminalStopEnv) == "1" {
		runGitTerminalStopHelper(t)
		return
	}

	directory := t.TempDir()
	marker := directory + "/ready"
	scriptPath := directory + "/git-terminal-stop-helper"
	contents := `#!/bin/sh
: > "$COMMIT_TEST_GIT_TERMINAL_MARKER"
sleep 30
`
	if err := os.WriteFile(scriptPath, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestGitCommandTerminalStopDoesNotHang$")
	cmd.Env = append(
		os.Environ(),
		gitTerminalStopEnv+"=1",
		gitTerminalScriptEnv+"="+scriptPath,
		gitTerminalMarkerEnv+"="+marker,
		"TERM=dumb",
		"NO_COLOR=1",
	)
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan []byte, 1)
	go func() {
		contents, _ := io.ReadAll(terminal)
		output <- contents
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = terminal.Close()
			_ = cmd.Wait()
			t.Fatalf("terminal stop helper did not start: %q", <-output)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := terminal.Write([]byte{0x1a}); err != nil {
		t.Fatal(err)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		_ = terminal.Close()
		contents := <-output
		if err != nil {
			t.Fatalf("terminal stop helper failed: %v\n%s", err, contents)
		}
		if !strings.Contains(string(contents), "terminal stop contained") {
			t.Fatalf("terminal stop helper output = %q", contents)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = terminal.Close()
		<-waited
		t.Fatalf("stopped Git process retained the terminal: %q", <-output)
	}
}

func runGitTerminalHelper(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := &gitOperations{gitPath: os.Getenv(gitTerminalScriptEnv)}
	if _, err := g.runGit(ctx, "", os.Stdin, "commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "terminal handoff succeeded"); err != nil {
		t.Fatal(err)
	}
}

func runGitFilterTerminalHelper(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gitOps := newStagingIntegrationGitOperations(t, os.Getenv(gitFilterRepoEnv))
	session, err := gitOps.BeginStaging(ctx, nil, []string{"prompt.txt"}, false)
	if err != nil {
		t.Fatal(err)
	}
	state := session
	t.Cleanup(func() { _ = gitOps.FinishStaging(session) })
	if len(session.Files) != 1 || session.Files[0] != "prompt.txt" {
		t.Fatalf("staged files = %q, want prompt.txt from buffered pathspec", session.Files)
	}
	staged, err := gitOps.runGit(ctx, state.PrivateIndexPath, nil, "show", ":prompt.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != "filter payload\n" {
		t.Fatalf("staged filtered content = %q", staged)
	}
	if err := gitOps.FinishStaging(session); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "staging filter handoff succeeded"); err != nil {
		t.Fatal(err)
	}
}

func runGitTerminalStopHelper(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	g := &gitOperations{gitPath: os.Getenv(gitTerminalScriptEnv)}
	_, err := g.runGit(ctx, "", os.Stdin, "commit")
	if !errors.Is(err, errGitCommandStopped) {
		t.Fatalf("stopped Git error = %v, want terminal-stop error", err)
	}
	if _, err := fmt.Fprintln(os.Stdout, "terminal stop contained"); err != nil {
		t.Fatal(err)
	}
}

func writeGitTerminalHelperScript(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/git-terminal-helper"
	contents := `#!/bin/sh
printf '\nterminal helper ready\n' >/dev/tty
IFS= read -r response </dev/tty
test "$response" = signed
`
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureTerminalOutput(terminal *os.File, marker string) (<-chan struct{}, <-chan []byte) {
	ready := make(chan struct{})
	output := make(chan []byte, 1)
	go func() {
		var contents bytes.Buffer
		buffer := make([]byte, 256)
		readySent := false
		for {
			n, readErr := terminal.Read(buffer)
			contents.Write(buffer[:n])
			if !readySent && bytes.Contains(contents.Bytes(), []byte(marker)) {
				close(ready)
				readySent = true
			}
			if readErr != nil {
				output <- contents.Bytes()
				return
			}
		}
	}()
	return ready, output
}
