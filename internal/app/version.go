package app

import (
	_ "embed" // use embed!
	"runtime/debug"
	"time"
)

var Build string
var appGitCommit string
var appBuildEpochString string

func init() {
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, kv := range info.Settings {
			switch kv.Key {
			case "vcs.revision":
				appGitCommit = kv.Value
			case "vcs.time":
				LastCommit, _ := time.Parse(time.RFC3339, kv.Value)
				appBuildEpochString = LastCommit.String()
			}
		}
	}
}
