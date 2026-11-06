package envexport_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/envexport"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var cases = map[string]struct {
	env  []string
	args []string
}{
	"s3": {
		env: []string{
			"RESTIC_REPOSITORY=s3:https://s3.example.com/bucket/app",
			"RESTIC_PASSWORD=pa ss'word",
			"AWS_ACCESS_KEY_ID=AKIA123",
			"AWS_SECRET_ACCESS_KEY=s3cr3t",
			"AWS_DEFAULT_REGION=eu-central-1",
			"RESTIC_CACHE_DIR=/tmp/cache",
			// not for restic
			"RESTIC_BACKUP_ARGS=--one-file-system",
			"CRON_SCHEDULE=0 2 * * *",
			"B2_ACCOUNT_KEY=other-backend",
		},
		args: []string{"-o", "s3.list-objects-v1=true"},
	},
	"s3-role": {
		env: []string{"RESTIC_REPOSITORY=s3:s3.example.com/bucket", "RESTIC_PASSWORD_FILE=/run/secrets/pw"},
	},
	"rest": {
		env: []string{"RESTIC_REPOSITORY=rest:https://user:hunter2@backup.example.com:8000/app", "RESTIC_PASSWORD_COMMAND=pass show restic", "HTTPS_PROXY=http://proxy:3128"},
	},
	"gs": {
		env: []string{"RESTIC_REPOSITORY=gs:bucket:/app", "RESTIC_PASSWORD=pw", "GOOGLE_PROJECT_ID=proj", "GOOGLE_APPLICATION_CREDENTIALS=/etc/gcp/key.json"},
	},
	"azure": {
		env: []string{"RESTIC_REPOSITORY=azure:container:/app", "RESTIC_PASSWORD=pw", "AZURE_ACCOUNT_NAME=acct"},
	},
	"sftp": {
		env: []string{"RESTIC_REPOSITORY=sftp:backup@host:/srv/restic", "RESTIC_PASSWORD=pw"},
	},
	"local": {
		env: []string{"RESTIC_REPOSITORY=/srv/restic", "RESTIC_PASSWORD=pw"},
	},
	"repository-file": {
		env: []string{"RESTIC_REPOSITORY_FILE=/run/secrets/repo", "RESTIC_PASSWORD=pw", "AWS_ACCESS_KEY_ID=AKIA123", "B2_ACCOUNT_ID=b2id"},
	},
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // test data
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // test data
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestFormats(t *testing.T) {
	for name, c := range cases {
		info := envexport.Build(c.env, c.args, "0.17", true)
		for _, style := range envexport.Styles() {
			t.Run(name+"/"+string(style), func(t *testing.T) {
				var buf bytes.Buffer
				if err := envexport.Write(&buf, info, style); err != nil {
					t.Fatal(err)
				}
				golden(t, name+"."+string(style), buf.Bytes())
			})
		}
	}
}

func names(info service.EnvInfo) []string {
	var out []string
	for _, v := range info.Vars {
		out = append(out, v.Name)
	}
	return out
}

func TestSelection(t *testing.T) {
	got := names(envexport.Build(cases["s3"].env, nil, "0.17", true))
	// repository and password come first, the rest is sorted
	want := []string{"RESTIC_REPOSITORY", "RESTIC_PASSWORD", "AWS_ACCESS_KEY_ID", "AWS_DEFAULT_REGION", "AWS_SECRET_ACCESS_KEY", "RESTIC_CACHE_DIR"}
	if !slices.Equal(got, want) {
		t.Errorf("variables = %v, want %v (nothing of mcs-backup or other backends)", got, want)
	}
}

func TestMasking(t *testing.T) {
	env := append(slices.Clone(cases["s3"].env), "RESTIC_PASSWORD_FILE=/run/pw")
	masked := envexport.Build(env, nil, "0.17", false)
	byName := map[string]service.EnvVar{}
	for _, v := range masked.Vars {
		byName[v.Name] = v
	}
	for _, n := range []string{"RESTIC_PASSWORD", "AWS_SECRET_ACCESS_KEY"} {
		if v := byName[n]; v.Value != envexport.Mask || !v.Secret {
			t.Errorf("%s = %+v, want masked secret", n, v)
		}
	}
	for _, n := range []string{"AWS_ACCESS_KEY_ID", "RESTIC_PASSWORD_FILE", "AWS_DEFAULT_REGION"} {
		if v := byName[n]; v.Secret || v.Value == envexport.Mask {
			t.Errorf("%s = %+v, want plain", n, v)
		}
	}

	// the same names as in the logs, see redact.Secret
	rclone := envexport.Build([]string{"RESTIC_REPOSITORY=rclone:remote:app", "RCLONE_CONFIG_PASS=rclone-pass", "RCLONE_SFTP_KEY_USE_AGENT=true"}, nil, "0.17", false)
	if v := rclone.Vars[1]; v.Name != "RCLONE_CONFIG_PASS" || v.Value != envexport.Mask || !v.Secret {
		t.Errorf("%+v, want a masked secret", v)
	}
	if v := rclone.Vars[2]; v.Value != "true" || v.Secret {
		t.Errorf("%+v, want plain", v)
	}

	rest := envexport.Build(cases["rest"].env, nil, "0.17", false)
	if v := rest.Vars[0]; !v.Secret || strings.Contains(v.Value, "hunter2") {
		t.Errorf("repository %+v leaks the password in its URL", v)
	}
	if v := envexport.Build(cases["rest"].env, nil, "0.17", true).Vars[0]; !strings.Contains(v.Value, "hunter2") {
		t.Errorf("revealed repository = %q", v.Value)
	}
}

func TestMaskedOutputHasNoSecrets(t *testing.T) {
	for name, c := range cases {
		var buf bytes.Buffer
		for _, style := range envexport.Styles() {
			if err := envexport.Write(&buf, envexport.Build(c.env, c.args, "0.17", false), style); err != nil {
				t.Fatal(err)
			}
		}
		for _, secret := range []string{"s3cr3t", "hunter2", "pa ss"} {
			if strings.Contains(buf.String(), secret) {
				t.Errorf("%s: masked output contains %q", name, secret)
			}
		}
	}
}

func TestParseStyle(t *testing.T) {
	if st, err := envexport.ParseStyle("dotenv"); err != nil || st != envexport.StyleDotenv {
		t.Errorf("ParseStyle(dotenv) = %v, %v", st, err)
	}
	if _, err := envexport.ParseStyle("yaml"); err == nil {
		t.Error("ParseStyle(yaml) succeeded")
	}
}
