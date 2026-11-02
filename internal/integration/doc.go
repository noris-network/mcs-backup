// Package integration holds tests that run mcs-backup against a real restic
// (and, for the append-only case, a real rest-server). They need the build tag
// integration and the environment variable RESTIC_BIN:
//
//	RESTIC_BIN=/usr/bin/restic REST_SERVER_BIN=/usr/bin/rest-server go test -tags integration ./internal/integration/
//
// The S3 tests (s3_test.go, s3ui_test.go) additionally need an S3 server and
// are skipped without MCS_TEST_S3_ENDPOINT. They drive the web UI and the TUI
// against a repository with a realistic history. TestS3Demo prepares the same
// kind of repository for trying the TUI and the web UI by hand, see s3demo_test.go.
package integration
