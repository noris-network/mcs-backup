// Package resticfake provides a scripted stand-in for the restic binary so
// that tests can exercise code that runs restic without a real repository.
package resticfake

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Response is what the fake prints and returns for one invocation.
type Response struct {
	Stdout string
	Stderr string
	Exit   int
	// Sleep delays the response. The delay is interrupted by SIGINT or
	// SIGTERM, in which case the fake exits with 130 like restic does.
	Sleep time.Duration
}

// Fake is a fake restic executable in a temporary directory.
type Fake struct {
	// Bin is the path of the executable.
	Bin string
	dir string
}

// script dispatches on the first argument, or on the first two joined by an
// underscore ("cat_config") if such a command was registered. Responses are
// handed out in order; the last one repeats.
const script = `#!/bin/sh
d='%s'
for a in "$@"; do printf '%%s\037' "$a"; done >> "$d/calls.log"
printf '\n' >> "$d/calls.log"
pwd > "$d/last.cwd"
env > "$d/last.env"
key="$1"
[ -f "$d/${1}_${2}.last" ] && key="${1}_${2}"
if [ ! -f "$d/$key.last" ]; then
  echo "fake restic: no response registered for: $*" >&2
  exit 99
fi
n=0
[ -f "$d/$key.n" ] && n=$(cat "$d/$key.n")
echo $((n + 1)) > "$d/$key.n"
last=$(cat "$d/$key.last")
[ "$n" -gt "$last" ] && n=$last
p="$d/$key.$n"
if [ -f "$p.sleep" ]; then
  trap 'kill $! 2>/dev/null; exit 130' INT TERM
  sleep "$(cat "$p.sleep")" &
  wait $!
fi
cat "$p.stdout"
cat "$p.stderr" >&2
exit "$(cat "$p.exit")"
`

// New creates a fake restic. It has no responses until On is called.
func New(t testing.TB) *Fake {
	t.Helper()
	dir := t.TempDir()
	f := &Fake{Bin: filepath.Join(dir, "restic"), dir: dir}
	if err := os.WriteFile(f.Bin, []byte(fmt.Sprintf(script, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// On registers the responses for a command. command is the first argument
// ("backup") or the first two joined by a space ("cat config").
func (f *Fake) On(t testing.TB, command string, responses ...Response) {
	t.Helper()
	key := strings.ReplaceAll(command, " ", "_")
	for i, r := range responses {
		p := filepath.Join(f.dir, fmt.Sprintf("%s.%d", key, i))
		files := map[string]string{
			p + ".stdout": r.Stdout,
			p + ".stderr": r.Stderr,
			p + ".exit":   strconv.Itoa(r.Exit),
		}
		if r.Sleep > 0 {
			files[p+".sleep"] = strconv.FormatFloat(r.Sleep.Seconds(), 'f', 3, 64)
		}
		for name, content := range files {
			if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	last := filepath.Join(f.dir, key+".last")
	if err := os.WriteFile(last, []byte(strconv.Itoa(len(responses)-1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Calls returns the arguments of every invocation so far.
func (f *Fake) Calls(t testing.TB) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
	}
	return calls
}

// CallsTo returns the invocations whose first argument is command.
func (f *Fake) CallsTo(t testing.TB, command string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range f.Calls(t) {
		if len(c) > 0 && c[0] == command {
			out = append(out, c)
		}
	}
	return out
}

// LastDir returns the working directory of the latest invocation.
func (f *Fake) LastDir(t testing.TB) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "last.cwd"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// LastEnv returns the environment of the latest invocation.
func (f *Fake) LastEnv(t testing.TB) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "last.env"))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}
