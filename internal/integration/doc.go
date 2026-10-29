// Package integration holds tests that run mcs-backup against a real restic
// (and, for the append-only case, a real rest-server). They need the build tag
// integration and the environment variable RESTIC_BIN:
//
//	RESTIC_BIN=/usr/bin/restic REST_SERVER_BIN=/usr/bin/rest-server go test -tags integration ./internal/integration/
package integration
