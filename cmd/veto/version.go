package main

import (
	"runtime/debug"
)

// buildProvenance is set to "official" only by the release packaging script.
// Versioned go install builds remain identifiable as source builds.
var buildProvenance = "source"

func resolveBuildVersion(linkerVersion string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	moduleVersion := ""
	info, ok := readBuildInfo()
	if ok && info != nil {
		moduleVersion = info.Main.Version
	}
	return effectiveVersion(linkerVersion, moduleVersion)
}
