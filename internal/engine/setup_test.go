package engine

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
)

func loadConfig(t *testing.T, env ...string) *config.Config {
	t.Helper()
	cfg, err := config.Load(append([]string{
		"PATH=" + os.Getenv("PATH"), "RESTIC_REPOSITORY=/main", "RESTIC_PASSWORD=main-pw", "AWS_ACCESS_KEY_ID=key",
		`RETENTION_POLICY={"last":3}`, "BACKUP_ROOT=" + t.TempDir(), "TZ=UTC", "MCS_BACKUP_WEB_TOKEN=tok",
	}, env...))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSecondEnv(t *testing.T) {
	cfg := loadConfig(t, "COPY_REPOSITORY=s3:host/offsite", "COPY_PASSWORD=copy-pw", "RESTIC_FROM_PASSWORD=stale", "RESTIC_PASSWORD_FILE=/run/main")
	env := SecondEnv(cfg)
	for _, want := range []string{"RESTIC_REPOSITORY=s3:host/offsite", "RESTIC_PASSWORD=copy-pw", "AWS_ACCESS_KEY_ID=key"} {
		if !slices.Contains(env, want) {
			t.Errorf("environment lacks %s", want)
		}
	}
	for _, kv := range env {
		if strings.Contains(kv, "main-pw") || strings.HasPrefix(kv, "RESTIC_PASSWORD_FILE=") || strings.HasPrefix(kv, "RESTIC_FROM_") ||
			strings.HasPrefix(kv, "MCS_BACKUP_") || strings.HasPrefix(kv, "COPY_") || kv == "RESTIC_REPOSITORY=/main" {
			t.Errorf("environment contains %q", kv)
		}
	}
	repos := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "RESTIC_REPOSITORY=") {
			repos++
		}
	}
	if repos != 1 {
		t.Errorf("%d repositories in the environment", repos)
	}

	cfg = loadConfig(t, "COPY_REPOSITORY=/second", "COPY_PASSWORD_FILE=/run/copy")
	env = SecondEnv(cfg)
	if !slices.Contains(env, "RESTIC_PASSWORD_FILE=/run/copy") {
		t.Errorf("env = %q", env)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "RESTIC_PASSWORD=") {
			t.Errorf("a password file replaces the password: %q", kv)
		}
	}
}

func TestNewFromConfigUsesTheConfiguredBinary(t *testing.T) {
	fake := resticfake.New(t)
	fake.On(t, "cat config", resticfake.Response{Stdout: `{"version":2,"id":"repo-id","chunker_polynomial":"x"}`})
	fake.On(t, "version", resticfake.Response{Stdout: "restic 0.19.1 compiled with go1.26 on linux/amd64"})
	cfg := loadConfig(t, "MCS_BACKUP_RESTIC="+fake.Bin, "S3_USE_V1=true")
	e := NewFromConfig(cfg, nil)

	info, err := e.OpenMain(t.Context())
	if err != nil || info.ID != "repo-id" {
		t.Fatalf("OpenMain() = %+v, %v", info, err)
	}
	call := fake.CallsTo(t, "cat")[0]
	if !slices.Contains(call, "s3.list-objects-v1=true") {
		t.Errorf("global arguments missing: %q", call)
	}
	env := fake.LastEnv(t)
	if env["RESTIC_REPOSITORY"] != "/main" || env["AWS_ACCESS_KEY_ID"] != "key" || env["MCS_BACKUP_WEB_TOKEN"] != "" {
		t.Errorf("env = %v", env)
	}
	if e.HasSecond() {
		t.Error("no secondary repository configured")
	}
	if got := fake.LastDir(t); !strings.HasPrefix(got, "/") {
		t.Errorf("dir = %q", got)
	}
}

func TestProbe(t *testing.T) {
	main, second := resticfake.New(t), resticfake.New(t)
	main.On(t, "version", resticfake.Response{Stdout: "restic 0.19.1 compiled with go1.26 on linux/amd64"})
	main.On(t, "cat config", resticfake.Response{Stdout: `{"version":2,"id":"main-id","chunker_polynomial":"x"}`})
	main.On(t, "snapshots", resticfake.Response{Stdout: "[]"})
	second.On(t, "cat config", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})

	cfg := loadConfig(t, "COPY_REPOSITORY=/second", "COPY_PASSWORD=x")
	e := New(Options{
		Config: cfg,
		Main:   restic.New(restic.Options{Bin: main.Bin, Env: cfg.ResticEnviron(), Dir: cfg.Root}),
		Second: restic.New(restic.Options{Bin: second.Bin, Env: SecondEnv(cfg), Dir: cfg.Root}),
	})

	v, res, err := e.Probe(t.Context())
	if err != nil || v.String() != "0.19.1" || len(res) != 2 {
		t.Fatalf("Probe() = %v, %+v, %v", v, res, err)
	}
	if res[0].Repo != "main" || res[0].ID != "main-id" || res[0].Err != nil || res[0].Missing {
		t.Errorf("main = %+v", res[0])
	}
	if res[1].Repo != "copy" || !res[1].Missing || res[1].Err != nil {
		t.Errorf("copy = %+v: a missing repository is created at the first start", res[1])
	}
	if call := main.CallsTo(t, "snapshots"); len(call) != 1 || slices.Contains(call[0], "--no-lock") {
		t.Errorf("the lock check must take a lock: %q", call)
	}
	if len(second.CallsTo(t, "snapshots")) != 0 {
		t.Error("a repository that does not exist is not locked")
	}

	// the lock cannot be released: typical for storage that forbids deleting
	main.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: unable to remove lock: 403 forbidden\n", Exit: 1})
	_, res, _ = e.Probe(t.Context())
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "deleting under locks/") || !strings.Contains(res[0].Err.Error(), "403") {
		t.Errorf("main = %+v", res[0])
	}

	// wrong password
	main.On(t, "cat config", resticfake.Response{Stderr: "Fatal: wrong password or no key found\n", Exit: 12})
	_, res, _ = e.Probe(t.Context())
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "wrong password") {
		t.Errorf("main = %+v", res[0])
	}
}
