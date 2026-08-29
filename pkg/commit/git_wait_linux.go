//go:build linux

package commit

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	linuxChildTrapped = 4 // CLD_TRAPPED from <signal.h>
	linuxChildStopped = 5 // CLD_STOPPED from <signal.h>
)

func awaitGitCommandExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(
			unix.P_PID,
			pid,
			&info,
			unix.WEXITED|unix.WSTOPPED|unix.WNOWAIT,
			nil,
		)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err == nil && (info.Code == linuxChildStopped || info.Code == linuxChildTrapped) {
			return errGitCommandStopped
		}
		return err
	}
}
