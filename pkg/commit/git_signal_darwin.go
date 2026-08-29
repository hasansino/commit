//go:build darwin

package commit

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	darwinSignalBlock   = 1
	darwinSignalSetMask = 3
)

// Darwin's sigset_t is a uint32. x/sys does not expose pthread_sigmask on
// Darwin, so call the kernel's thread-specific entry point while the caller is
// pinned to an OS thread.
func blockTerminalOutputSignal() (func() error, error) {
	blocked := uint32(1) << uint(unix.SIGTTOU-1)
	var previous uint32
	if err := darwinPthreadSigmask(darwinSignalBlock, &blocked, &previous); err != nil {
		return nil, err
	}
	return func() error {
		return darwinPthreadSigmask(darwinSignalSetMask, &previous, nil)
	}, nil
}

func darwinPthreadSigmask(how int, set, previous *uint32) error {
	// x/sys has no non-deprecated wrapper for Darwin's thread-specific mask.
	//nolint:staticcheck
	_, _, errno := unix.RawSyscall(
		unix.SYS___PTHREAD_SIGMASK,
		uintptr(how),
		uintptr(unsafe.Pointer(set)),
		uintptr(unsafe.Pointer(previous)),
	)
	if errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}
