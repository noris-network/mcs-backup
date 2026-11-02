//go:build integration

package integration

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// The S3 tests write to a real S3 server and need
//
//	MCS_TEST_S3_ENDPOINT    for example http://127.0.0.1:9000
//	MCS_TEST_S3_ACCESS_KEY, MCS_TEST_S3_SECRET_KEY
//	MCS_TEST_S3_BUCKET      optional, backuptest by default
//
// Without the endpoint they are skipped. Every run writes below
// mcs-backup-it/<run>/ in the bucket and leaves it there; empty the bucket
// from time to time.

type s3Settings struct {
	endpoint, bucket, access, secret string
}

func s3Config(t *testing.T) s3Settings {
	t.Helper()
	endpoint := os.Getenv("MCS_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("MCS_TEST_S3_ENDPOINT is not set: skipping the S3 tests")
	}
	s := s3Settings{
		endpoint: strings.TrimRight(endpoint, "/"),
		bucket:   cmp.Or(os.Getenv("MCS_TEST_S3_BUCKET"), "backuptest"),
		access:   os.Getenv("MCS_TEST_S3_ACCESS_KEY"),
		secret:   os.Getenv("MCS_TEST_S3_SECRET_KEY"),
	}
	if s.access == "" || s.secret == "" {
		t.Fatal("MCS_TEST_S3_ACCESS_KEY and MCS_TEST_S3_SECRET_KEY are needed with MCS_TEST_S3_ENDPOINT")
	}
	return s
}

// repo is the restic repository URL below prefix.
func (s s3Settings) repo(prefix string) string {
	return "s3:" + s.endpoint + "/" + s.bucket + "/" + prefix
}

func (s s3Settings) credentials() []string {
	return []string{"AWS_ACCESS_KEY_ID=" + s.access, "AWS_SECRET_ACCESS_KEY=" + s.secret}
}

// s3Run names the directory of this test run in the bucket.
var s3Run = time.Now().UTC().Format("20060102-150405")

// newS3Inst is newInst with a repository in the S3 bucket.
func newS3Inst(t *testing.T, name string, extra ...string) *inst {
	t.Helper()
	s := s3Config(t)
	i := newInst(t, extra...)
	i.repo = s.repo(path.Join("mcs-backup-it", s3Run, name))
	i.cloud = s.credentials()
	i.set("RESTIC_REPOSITORY=" + i.repo)
	i.set(i.cloud...)
	return i
}

// ---- test data ----

// blob writes n pseudo-random bytes that depend only on seed.
func (i *inst) blob(rel string, n int, seed uint64) {
	i.t.Helper()
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(seed, seed))
	for k := range b {
		b[k] = byte(r.Uint32())
	}
	i.write(rel, string(b))
}

// fillData writes the data of a small application: a database file, media and
// documents. generation changes the database.
func (i *inst) fillData(generation uint64) {
	i.t.Helper()
	i.write("README.md", "# Shop data\n\nSample data for trying out mcs-backup.\n")
	i.blob("db/main.dat", 300<<10, generation)
	i.blob("media/photo-1.jpg", 150<<10, 100)
	for n := range 20 {
		i.write(fmt.Sprintf("docs/note-%02d.txt", n), strings.Repeat("a line of text\n", 50+n))
	}
}

// cli runs a command of the binary that has to succeed.
func (i *inst) cli(args ...string) string {
	i.t.Helper()
	out, errOut, code := i.bin(args...)
	if code != 0 {
		i.t.Fatalf("mcs-backup %s: exit %d\n%s\n%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// cliSnapshots lists the snapshots through the running service.
func (i *inst) cliSnapshots() []service.Snapshot {
	i.t.Helper()
	var snaps []service.Snapshot
	if err := json.Unmarshal([]byte(i.cli("snapshots", "--json")), &snaps); err != nil {
		i.t.Fatalf("snapshots --json: %v", err)
	}
	return snaps
}

func findNote(snaps []service.Snapshot, note string) (service.Snapshot, bool) {
	for _, s := range snaps {
		if s.Note == note {
			return s, true
		}
	}
	return service.Snapshot{}, false
}

// history gives the repository of a running service the past of a real one:
// a protected first snapshot, regular backups, a backup that came out far
// too small (flagged suspicious) and an in-place restore with its safety
// snapshot. It leaves the data of the restored state plus one new file.
func (i *inst) history() {
	i.t.Helper()
	i.fillData(1)
	i.cli("backup", "--note", "initial import", "--keep")

	i.blob("db/main.dat", 300<<10, 2)
	i.blob("media/photo-2.jpg", 150<<10, 101)
	i.cli("backup", "--note", "nightly")

	i.write("reports/2026-Q3.csv", strings.Repeat("2026-09-30;orders;1234\n", 200))
	i.cli("backup", "--note", "after the release")

	// a volume that came up half empty: stored, but flagged
	i.remove("db")
	i.remove("media")
	i.bin("backup", "--note", "volume came up half empty")
	if s, ok := findNote(i.cliSnapshots(), "volume came up half empty"); !ok || !s.Suspicious {
		i.t.Fatalf("the shrunken backup is not flagged suspicious: %+v", s)
	}

	i.fillData(3)
	i.blob("media/photo-2.jpg", 150<<10, 101)
	i.cli("backup", "--note", "nightly")

	// go back to the first snapshot; the current data is saved first
	first, ok := findNote(i.cliSnapshots(), "initial import")
	if !ok {
		i.t.Fatal("the first snapshot is missing")
	}
	i.cli("restore", "--confirm", first.ShortID)
	if !hasPreRestore(i.cliSnapshots()) {
		i.t.Fatal("the restore left no safety snapshot")
	}
	i.write("inbox/unsaved.txt", "written after the last backup\n")
}

func hasPreRestore(snaps []service.Snapshot) bool {
	for _, s := range snaps {
		if s.PreRestore {
			return true
		}
	}
	return false
}
