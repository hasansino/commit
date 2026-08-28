//go:build windows

package commit

import "golang.org/x/sys/windows"

func replaceFile(source, destination string) error {
	return windows.Rename(source, destination)
}
