//go:build windows

package commit

import "os/exec"

// CommandContext terminates the direct Git process on Windows. Released
// targets are Linux and macOS; a Windows job-object implementation is needed
// before descendant-tree termination can offer the same guarantee there.
func configureGitCommandCancellation(_ *exec.Cmd) {}

func terminateGitCommandTree(_ *exec.Cmd) (bool, error) {
	return false, nil
}

func runConfiguredGitCommand(cmd *exec.Cmd, _ bool) error {
	return cmd.Run()
}
