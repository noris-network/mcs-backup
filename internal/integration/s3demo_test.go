//go:build integration

package integration

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// TestS3Demo fills a repository in the S3 bucket with a realistic history and
// prepares a directory from which the TUI and the web UI can be tried by
// hand, with real data. It only runs with MCS_TEST_S3_DEMO=1 (and the S3
// settings, see s3_test.go):
//
//	MCS_TEST_S3_DEMO=1 go test -tags integration -run TestS3Demo -v ./internal/integration/
//
// The directory (MCS_TEST_S3_DEMO_DIR, local/s3-demo by default) gets
//
//	mcs-backup   a script that runs the binary with the environment of the demo
//	serve.env    that environment, with the credentials (mode 0600)
//	data/        the BACKUP_ROOT
//	bin/         the binary
//
// MCS_TEST_S3_DEMO_LOCK=1 additionally leaves a lock of a dead restic process
// in the repository, for the Locks tab.
func TestS3Demo(t *testing.T) {
	if os.Getenv("MCS_TEST_S3_DEMO") == "" {
		t.Skip("MCS_TEST_S3_DEMO is not set: not preparing the demo")
	}
	s := s3Config(t)
	dir, err := filepath.Abs(cmp.Or(os.Getenv("MCS_TEST_S3_DEMO_DIR"), "../../local/s3-demo"))
	if err != nil {
		t.Fatal(err)
	}
	// replace an earlier demo, but never a directory that is something else
	if _, err := os.Stat(dir); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "serve.env")); err != nil {
			t.Fatalf("%s exists and is not a demo directory; remove it or choose another MCS_TEST_S3_DEMO_DIR", dir)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{"data", "state", "cache", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	prefix := path.Join("mcs-backup-demo", s3Run)
	demo := []string{
		"TZ=Europe/Berlin",
		"RESTIC_REPOSITORY=" + s.repo(prefix),
		"RESTIC_PASSWORD=demo-password",
		"COPY_REPOSITORY=" + s.repo(prefix+"-copy"),
		"COPY_PASSWORD=demo-copy-password",
		"BACKUP_ROOT=" + filepath.Join(dir, "data"),
		"RESTIC_CACHE_DIR=" + filepath.Join(dir, "cache"),
		"MCS_BACKUP_STATE_DIR=" + filepath.Join(dir, "state"),
		"MCS_BACKUP_SOCKET=" + filepath.Join(dir, "s.sock"),
		"MCS_BACKUP_RESTIC=" + resticBin,
		`RETENTION_POLICY={"last":10,"daily":7}`,
		"CRON_SCHEDULE=15 2 * * *",
		"BACKUP_HTTP_PORT=9100", // 9000 is the S3 server
		"MCS_BACKUP_WEB_ADDR=127.0.0.1:8484",
	}
	demo = append(demo, s.credentials()...)

	i := newInst(t, demo...)
	i.root = filepath.Join(dir, "data")
	i.repo = s.repo(prefix)
	i.cloud = s.credentials()
	i.serve()
	i.history()

	if os.Getenv("MCS_TEST_S3_DEMO_LOCK") != "" {
		i.holdLock()() // frozen, then killed at the end of the test: the lock stays
	}
	if err := copyFile(mcsBin, filepath.Join(dir, "bin", "mcs-backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileMode(t, filepath.Join(dir, "serve.env"), envFile(demo), 0o600)
	writeFileMode(t, filepath.Join(dir, "mcs-backup"), wrapper, 0o755)

	t.Logf("demo repository: %s", s.repo(prefix))
	t.Logf("run it:\n  %s/mcs-backup serve      # in one terminal\n  %s/mcs-backup tui        # in another one\n  %s/mcs-backup web        # the web UI", dir, dir, dir)
}

// wrapper runs the binary with the environment of the demo.
const wrapper = `#!/bin/sh
# Runs mcs-backup with the environment of the S3 demo: serve, tui, web, status, ...
dir=$(cd "$(dirname "$0")" && pwd)
set -a
. "$dir/serve.env"
set +a
exec "$dir/bin/mcs-backup" "$@"
`

// envFile renders KEY=VALUE pairs for a shell to source.
func envFile(kv []string) string {
	var b strings.Builder
	b.WriteString("# environment of the mcs-backup S3 demo; contains credentials\n")
	for _, p := range kv {
		k, v, _ := strings.Cut(p, "=")
		fmt.Fprintf(&b, "%s='%s'\n", k, strings.ReplaceAll(v, "'", `'\''`))
	}
	return b.String()
}

func writeFileMode(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), mode); err != nil { //nolint:gosec // demo files
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}

func copyFile(from, to string, mode os.FileMode) (err error) {
	src, err := os.Open(from) //nolint:gosec // the binary built by TestMain
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec // demo files
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dst.Close()) }()
	_, err = io.Copy(dst, src)
	return err
}
