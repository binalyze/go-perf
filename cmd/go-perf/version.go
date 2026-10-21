package main

import (
	"fmt"
	"runtime/debug"
)

// Set at build time, e.g.
// go build -ldflags "-X main.version=v1.2.3 -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +%FT%TZ)"
var (
	version   = ""
	commit    = ""
	buildTime = ""
)

func versionString() string {
	v, c, t := version, commit, buildTime
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" && info.Main.Version != "" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && t == "":
				t = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if len(c) > 12 {
		c = c[:12]
	}
	return fmt.Sprintf("%s Build:%s Commit:%s", v, t, c)
}
