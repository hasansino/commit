//go:build darwin

package commit

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

const darwinProcessStopped = 4 // SSTOP from <sys/proc.h>

func awaitGitCommandExit(pid int) error {
	kqueue, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Close(kqueue)
	}()

	var change unix.Kevent_t
	unix.SetKevent(
		&change,
		pid,
		unix.EVFILT_PROC,
		unix.EV_ADD|unix.EV_CLEAR,
	)
	change.Fflags = unix.NOTE_EXIT | unix.NOTE_SIGNAL
	events := make([]unix.Kevent_t, 1)
	changes := []unix.Kevent_t{change}
	for {
		n, eventErr := unix.Kevent(kqueue, changes, events, nil)
		changes = nil
		if errors.Is(eventErr, syscall.EINTR) {
			continue
		}
		// A fast child can exit before EVFILT_PROC attaches. Darwin's
		// proc_find excludes zombies, so registration then returns ESRCH.
		// We have not called Wait: the unreaped child still reserves its PID.
		if errors.Is(eventErr, syscall.ESRCH) {
			return nil
		}
		if eventErr != nil {
			return eventErr
		}
		if n != 1 {
			return fmt.Errorf("unexpected process event count %d", n)
		}
		event := events[0]
		if event.Flags&unix.EV_ERROR != 0 && event.Data != 0 {
			// Registration errors normally arrive in the event when there is
			// space in the event list, rather than as the syscall's error.
			if event.Data == int64(syscall.ESRCH) {
				return nil
			}
			// #nosec G115 -- EV_ERROR data is a positive kernel errno, which fits uintptr on supported Darwin targets.
			return syscall.Errno(event.Data)
		}
		if event.Fflags&unix.NOTE_EXIT != 0 {
			return nil
		}
		if event.Fflags&unix.NOTE_SIGNAL != 0 {
			process, statusErr := unix.SysctlKinfoProc("kern.proc.pid", pid)
			if statusErr == nil && process.Proc.P_stat == darwinProcessStopped {
				return errGitCommandStopped
			}
		}
	}
}
