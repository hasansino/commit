//go:build linux

package commit

import "golang.org/x/sys/unix"

func blockTerminalOutputSignal() (func() error, error) {
	var blocked unix.Sigset_t
	blocked.Val[0] = 1 << uint(unix.SIGTTOU-1)
	var previous unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &blocked, &previous); err != nil {
		return nil, err
	}
	return func() error {
		return unix.PthreadSigmask(unix.SIG_SETMASK, &previous, nil)
	}, nil
}
