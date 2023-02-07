package app

import _ "embed" // use embed!

//go:embed .VERSION
var appVersion string

//go:embed .GIT_COMMIT
var appGitCommit string

//go:embed .BUILD_EPOCH
var appBuildEpochString string
