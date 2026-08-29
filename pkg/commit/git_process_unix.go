//go:build linux || darwin

package commit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var gitTerminalForegroundMu sync.Mutex

var errGitCommandStopped = errors.New("git process group stopped while it owned the terminal")

func configureGitCommandCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		terminated, err := terminateGitCommandTree(cmd)
		if err != nil {
			return err
		}
		if !terminated {
			return os.ErrProcessDone
		}
		return nil
	}
}

func terminateGitCommandTree(cmd *exec.Cmd) (bool, error) {
	if cmd.Process == nil {
		return false, nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func runConfiguredGitCommand(cmd *exec.Cmd, mayUseTerminal bool) error {
	if !mayUseTerminal {
		if err := cmd.Start(); err != nil {
			return err
		}
		return waitForGitCommandAndCleanup(cmd)
	}

	// Only one child can own the controlling terminal's foreground process
	// group. Commands without a controlling terminal retain normal exec
	// behavior; there is no terminal job-control signal to avoid in that case.
	gitTerminalForegroundMu.Lock()
	defer gitTerminalForegroundMu.Unlock()

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if err := cmd.Start(); err != nil {
			return err
		}
		return waitForGitCommandAndCleanup(cmd)
	}
	defer tty.Close()

	ttyFD := int(tty.Fd())
	foregroundGroup, err := unix.IoctlGetInt(ttyFD, unix.TIOCGPGRP)
	if err != nil || foregroundGroup != syscall.Getpgrp() {
		// If this process is itself a background job, taking the terminal away
		// from its foreground owner would violate the caller's job control.
		if err := cmd.Start(); err != nil {
			return err
		}
		return waitForGitCommandAndCleanup(cmd)
	}

	// Foreground performs setpgid and tcsetpgrp in the child between fork and
	// exec while job-control signals are blocked. This avoids the race where a
	// signer or credential helper reads the TTY before the parent can hand it
	// over and is stopped with SIGTTIN.
	cmd.SysProcAttr.Foreground = true
	cmd.SysProcAttr.Ctty = ttyFD
	if err := cmd.Start(); err != nil {
		return err
	}

	// tcsetpgrp from the now-background parent would normally raise SIGTTOU.
	// Block it only on this locked OS thread, and only after Start, so Git and
	// its hooks do not inherit the blocked signal.
	runtime.LockOSThread()
	restoreSignalMask, maskErr := blockTerminalOutputSignal()
	if maskErr != nil {
		runtime.UnlockOSThread()
		return stopAndWaitForGitCommand(cmd, maskErr)
	}

	terminalSet := true
	childForeground, foregroundErr := unix.IoctlGetInt(ttyFD, unix.TIOCGPGRP)
	var runErr error
	if foregroundErr != nil || childForeground != cmd.Process.Pid {
		if foregroundErr == nil {
			foregroundErr = fmt.Errorf(
				"foreground process group is %d, want %d",
				childForeground,
				cmd.Process.Pid,
			)
		}
		runErr = stopAndWaitForGitCommand(
			cmd,
			fmt.Errorf("failed to verify git terminal handoff: %w", foregroundErr),
		)
	} else {
		runErr = waitForGitCommandAndCleanup(cmd)
	}

	var restoreTerminalErr error
	if terminalSet {
		if err := unix.IoctlSetPointerInt(ttyFD, unix.TIOCSPGRP, foregroundGroup); err != nil {
			restoreTerminalErr = fmt.Errorf("failed to restore terminal foreground group: %w", err)
		}
	}
	restoreMaskErr := restoreSignalMask()
	runtime.UnlockOSThread()

	if restoreMaskErr != nil {
		restoreMaskErr = fmt.Errorf("failed to restore terminal signal mask: %w", restoreMaskErr)
	}
	return errors.Join(runErr, restoreTerminalErr, restoreMaskErr)
}

func waitForGitCommandAndCleanup(cmd *exec.Cmd) error {
	// Observe Git's exit without reaping it. The zombie continues to reserve
	// its process-group ID, so killing that group cannot race with PID reuse.
	// This also terminates hooks that intentionally close their capture pipes
	// and remain in Git's process group after Git itself has exited.
	if err := awaitGitCommandExit(cmd.Process.Pid); err != nil {
		return stopAndWaitForGitCommand(
			cmd,
			fmt.Errorf("failed to observe git process exit safely: %w", err),
		)
	}
	_, terminateErr := terminateGitCommandTree(cmd)
	if errors.Is(terminateErr, syscall.EPERM) {
		// Darwin reports EPERM when the only remaining group member is the
		// exited (and therefore unsignalable) Git zombie. Any ordinary hook
		// descendant inherited our credentials and would make killpg succeed.
		terminateErr = nil
	}
	waitErr := cmd.Wait()
	if terminateErr != nil {
		terminateErr = fmt.Errorf("failed to terminate surviving git descendants: %w", terminateErr)
	}
	return errors.Join(waitErr, terminateErr)
}

func stopAndWaitForGitCommand(cmd *exec.Cmd, cause error) error {
	_, terminateErr := terminateGitCommandTree(cmd)
	waitErr := cmd.Wait()
	return errors.Join(cause, terminateErr, waitErr)
}
