package version

import (
	"runtime/debug"
)

// set during compile time
var xBuildVersion = "dev"

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
