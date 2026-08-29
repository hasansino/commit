//go:build !linux && !darwin && !windows

package commit

import "os/exec"

func configureGitCommandCancellation(_ *exec.Cmd) {}

func terminateGitCommandTree(_ *exec.Cmd) (bool, error) {
	return false, nil
}

func runConfiguredGitCommand(cmd *exec.Cmd, _ bool) error {
	return cmd.Run()
}
