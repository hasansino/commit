package version

import (
	"runtime/debug"
)

// set during compile time
var (
	xBuildVersion = "dev"
	xBuildCommit  = "dev"
)

func GetVersion() string {
	if xBuildVersion != "dev" {
		return xBuildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "(devel)" && info.Main.Version != "" {
			return info.Main.Version
		}
	}
	return xBuildVersion
}

// GetCommit returns the source commit of the binary: the release commit set at build
// time, or the VCS revision that the Go toolchain recorded for a checkout build.
func GetCommit() string {
	if xBuildCommit != "dev" {
		return xBuildCommit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value
			}
		}
	}
	return xBuildCommit
}
