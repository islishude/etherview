package main

import (
	"runtime/debug"
	"strings"
)

func buildMetadata() (version, revision, buildDate string) {
	version = "dev"
	revision = "unknown"
	buildDate = "unknown"

	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil {
		return
	}

	v := strings.TrimSpace(info.Main.Version)
	if v != "" && v != "(devel)" {
		version = v
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			value := strings.TrimSpace(setting.Value)
			if value != "" {
				revision = value
			}
		case "vcs.time":
			value := strings.TrimSpace(setting.Value)
			if value != "" {
				buildDate = value
			}
		}
	}

	return
}
