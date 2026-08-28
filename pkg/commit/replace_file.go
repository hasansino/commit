//go:build !windows

package commit

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
