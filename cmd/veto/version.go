package main

import (
	"runtime/debug"
	"strings"
)

// buildProvenance is set to "official" only by the release packaging script.
// Versioned go install builds remain identifiable as source builds.
var buildProvenance = "source"

func resolvedVersion() string {
	return resolveBuildVersion(version, debug.ReadBuildInfo)
}

func resolveBuildVersion(linkerVersion string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if linkerVersion != "" && linkerVersion != "dev" {
		return strings.TrimPrefix(linkerVersion, "v")
	}
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return "dev"
	}
	moduleVersion := strings.TrimSpace(info.Main.Version)
	if moduleVersion == "" || moduleVersion == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(moduleVersion, "v")
}
