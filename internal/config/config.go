// Package config loads and validates the mcs-backup configuration from the
// environment and an optional YAML file.
package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config is the validated runtime configuration.
type Config struct {
	// Repository is the main restic repository. It is empty when the
	// repository is given as RESTIC_REPOSITORY_FILE.
	Repository string
	AppendOnly bool
	// Copy is the optional secondary repository, nil if not configured.
	Copy *Copy

	Root      string   // BACKUP_ROOT, working directory of backups and restores
	Paths     []string // BACKUP_PATHS, relative to Root
	Excludes  []string // EXCLUDE_PATHS
	Host      string   // MCS_BACKUP_HOST, restic host name of this instance
	Retention Retention

	Hooks Hooks
	Pipe  Pipe

	Schedule Schedule
	Timeouts Timeouts
	Backup   BackupPolicy
	Verify   Verify
	Notify   Notify
	Server   Server

	// AutoUnlockAfter removes locks if all are older than this; 0 disables it.
	AutoUnlockAfter time.Duration

	Restic        ResticArgs
	MetricsLabels map[string]string
	LogFormat     string // "json" or "text"
	Debug         bool

	// Warnings are non-fatal findings, e.g. ignored 1.x variables.
	Warnings []string

	env map[string]string
}

// Copy is the secondary repository that every backup is copied to.
type Copy struct {
	Repository   string
	Password     string
	PasswordFile string
	AppendOnly   bool
}

// Hooks are the scripts run around backups and restores.
type Hooks struct {
	PreBackup   string
	PostBackup  string
	PreRestore  string
	PostRestore string
}

// Pipe configures backing up and restoring a stream instead of files.
type Pipe struct {
	InScript   string // writes the data to stdout
	OutScript  string // reads the data from stdin
	InFilename string // file name of the stream inside the snapshot
}

// Schedule holds cron expressions. An empty expression means "not scheduled".
type Schedule struct {
	Backup     string
	BackupFile string
	Prune      string
	Check      string
	Verify     string
	// Location is the time zone all expressions are evaluated in.
	Location *time.Location
	CatchUp  bool
}

// Timeouts limit the duration of operations; 0 means no limit.
type Timeouts struct {
	Backup   time.Duration
	Prune    time.Duration
	Check    time.Duration
	Verify   time.Duration
	Restore  time.Duration
	PostHook time.Duration
	Shutdown time.Duration
}

// BackupPolicy tunes retries and the safety checks around backups.
type BackupPolicy struct {
	Retries    int
	RetryDelay time.Duration
	// AllowEmpty disables the check for empty snapshots.
	AllowEmpty bool
	// MaxShrink is the percentage by which a backup may shrink compared to the
	// last regular snapshot before it counts as suspicious; 0 disables it.
	MaxShrink float64
	// ForgetMaxDelete is the percentage of snapshots a single forget may
	// delete before it is held back for approval.
	ForgetMaxDelete float64
}

// Verify configures the restore test.
type Verify struct {
	Mode        string // "sample" or "full"
	SampleFiles int
	SampleBytes int64
	Dir         string // empty: a temporary directory
	Script      string
	Copy        bool
}

// Notify configures the external notification command.
type Notify struct {
	Command string
	On      []string
	Timeout time.Duration
}

// Server configures the listeners and local state.
type Server struct {
	HTTPPort int
	Socket   string
	StateDir string
	WebAddr  string // empty: web UI off at start
	WebToken string
}

// ResticArgs are additional arguments appended to restic invocations.
type ResticArgs struct {
	// Bin is the restic executable (MCS_BACKUP_RESTIC), "restic" by default.
	Bin     string
	Global  []string
	Backup  []string
	Forget  []string
	Prune   []string
	Check   []string
	Restore []string
}

// DefaultSocket is the socket path when MCS_BACKUP_SOCKET is not set.
func DefaultSocket() string { return filepath.Join(os.TempDir(), "mcs-backup.sock") }

// PipeMode reports whether data is backed up as a stream.
func (c *Config) PipeMode() bool { return c.Pipe.InScript != "" }

// Env returns the value of a configuration variable (environment or file).
func (c *Config) Env(key string) string { return c.env[key] }

// Environ returns the merged environment as KEY=VALUE pairs, sorted by key.
func (c *Config) Environ() []string {
	return environ(c.env, func(string) bool { return true })
}

// ResticEnviron returns the environment for restic processes: everything
// except the mcs-backup specific settings and the secondary repository
// credentials, which are passed explicitly where needed.
func (c *Config) ResticEnviron() []string {
	return environ(c.env, func(k string) bool {
		return !strings.HasPrefix(k, "MCS_BACKUP_") && !strings.HasPrefix(k, "COPY_")
	})
}

func environ(env map[string]string, keep func(string) bool) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if keep(k) {
			out = append(out, k+"="+env[k])
		}
	}
	return out
}

// Load builds the configuration from environ (KEY=VALUE pairs, normally
// os.Environ()). Settings from the YAML file named by MCS_BACKUP_CONFIG fill
// in what the environment does not set. All problems are reported together.
func Load(environ []string) (*Config, error) {
	env := parseEnviron(environ)
	if path := env["MCS_BACKUP_CONFIG"]; path != "" {
		if err := mergeFile(env, path); err != nil {
			return nil, fmt.Errorf("MCS_BACKUP_CONFIG: %w", err)
		}
	}
	l := &loader{env: env}
	c := l.config()
	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	c.Warnings = l.warns
	c.env = env
	return c, nil
}

// legacyIgnored are 1.x variables that no longer have an effect.
func legacyIgnored() []string {
	return []string{"INFLUXDB_URL", "INFLUXDB_DATABASE", "INFLUXDB_TOKEN", "INFLUXDB_ORG", "LOKI_URL", "S3_METRICS_TIMEOUT"}
}

func (l *loader) config() *Config {
	c := &Config{}

	// time zone
	c.Schedule.Location = time.Local
	if tz := l.str("TZ", ""); tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			l.fail("TZ", err)
		} else {
			c.Schedule.Location = loc
		}
	}

	// repositories
	c.Repository = l.repository()
	if c.Repository == "" && l.str("RESTIC_REPOSITORY_FILE", "") == "" && !l.failed("RESTIC_REPOSITORY") {
		l.fail("RESTIC_REPOSITORY", errors.New("not set (alternatively set RESTIC_REPOSITORY_BASE and RESTIC_REPOSITORY_PATH)"))
	}
	l.requirePassword("RESTIC_PASSWORD")
	c.AppendOnly = l.boolean("APPEND_ONLY", false)
	c.Copy = l.copyConfig()

	// data
	c.Root = l.root()
	c.Paths = l.list("BACKUP_PATHS", []string{"."})
	c.Excludes = l.list("EXCLUDE_PATHS", nil)
	c.Host = l.str("MCS_BACKUP_HOST", "mcs-backup")
	c.Retention = l.retention(c.AppendOnly)

	// hooks and pipes
	c.Hooks = Hooks{
		PreBackup:   l.script("PRE_BACKUP_SCRIPT"),
		PostBackup:  l.script("POST_BACKUP_SCRIPT"),
		PreRestore:  l.script("PRE_RESTORE_SCRIPT"),
		PostRestore: l.script("POST_RESTORE_SCRIPT"),
	}
	c.Pipe = Pipe{
		InScript:   l.script("PIPE_IN_SCRIPT"),
		OutScript:  l.script("PIPE_OUT_SCRIPT"),
		InFilename: l.str("PIPE_IN_FILENAME", "stdin"),
	}
	if (c.Pipe.InScript == "") != (c.Pipe.OutScript == "") {
		l.warn("only one of PIPE_IN_SCRIPT and PIPE_OUT_SCRIPT is set: backups and restores will not match")
	}

	// schedules
	c.Schedule.Backup = l.schedule("CRON_SCHEDULE", "", c.Schedule.Location)
	c.Schedule.BackupFile = l.scheduleFile(c.Schedule.Backup, c.Schedule.Location)
	c.Schedule.Prune = l.schedule("PRUNE_SCHEDULE", "0 3 * * 0", c.Schedule.Location)
	c.Schedule.Check = l.schedule("CHECK_SCHEDULE", "0 4 1 * *", c.Schedule.Location)
	c.Schedule.Verify = l.schedule("VERIFY_SCHEDULE", "0 5 * * 6", c.Schedule.Location)
	c.Schedule.CatchUp = l.boolean("BACKUP_CATCH_UP", true)
	l.pruneInterval(c)

	// timeouts and policies
	c.Timeouts = Timeouts{
		Backup:   l.duration("BACKUP_TIMEOUT", 12*time.Hour),
		Prune:    l.duration("PRUNE_TIMEOUT", 12*time.Hour),
		Check:    l.duration("CHECK_TIMEOUT", 12*time.Hour),
		Verify:   l.duration("VERIFY_TIMEOUT", 12*time.Hour),
		Restore:  l.duration("RESTORE_TIMEOUT", 0),
		PostHook: l.duration("POST_HOOK_TIMEOUT", 5*time.Minute),
		Shutdown: l.duration("SHUTDOWN_TIMEOUT", 25*time.Second),
	}
	c.Backup = BackupPolicy{
		Retries:         l.integer("BACKUP_RETRIES", 2, 0),
		RetryDelay:      l.duration("BACKUP_RETRY_DELAY", 5*time.Minute),
		AllowEmpty:      l.boolean("ALLOW_EMPTY_SNAPSHOT", false),
		MaxShrink:       l.percent("BACKUP_MAX_SHRINK", 50),
		ForgetMaxDelete: l.percent("FORGET_MAX_DELETE", 25),
	}
	c.AutoUnlockAfter = l.duration("AUTO_UNLOCK_AFTER", 0)
	if c.AutoUnlockAfter > 0 && c.AutoUnlockAfter < 10*time.Minute {
		l.warn("AUTO_UNLOCK_AFTER is below 10m: locks of running restic processes could be removed (restic refreshes locks every 5m)")
	}

	c.Verify = l.verify(c)
	c.Notify = l.notify()
	c.Server = l.server()
	c.Restic = l.resticArgs()
	c.MetricsLabels = l.metricsLabels()

	c.LogFormat = l.str("MCS_BACKUP_LOG_FORMAT", "json")
	if c.LogFormat != "json" && c.LogFormat != "text" {
		l.fail("MCS_BACKUP_LOG_FORMAT", fmt.Errorf("%q is not one of json, text", c.LogFormat))
	}
	c.Debug = l.boolean("MCS_BACKUP_DEBUG", false)

	for _, k := range legacyIgnored() {
		if l.str(k, "") != "" {
			l.warn(k + " is no longer supported and ignored")
		}
	}
	return c
}

type loader struct {
	env        map[string]string
	errs       []error
	warns      []string
	failedKeys map[string]bool
}

func (l *loader) fail(key string, err error) {
	l.errs = append(l.errs, fmt.Errorf("%s: %w", key, err))
	if l.failedKeys == nil {
		l.failedKeys = map[string]bool{}
	}
	l.failedKeys[key] = true
}

func (l *loader) failed(key string) bool { return l.failedKeys[key] }

func (l *loader) warn(msg string) { l.warns = append(l.warns, msg) }

// str returns the trimmed value of key or def if unset or empty.
func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.env[key]); v != "" {
		return v
	}
	return def
}

func (l *loader) boolean(key string, def bool) bool {
	v := l.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(key, fmt.Errorf("invalid boolean %q", v))
		return def
	}
	return b
}

func (l *loader) integer(key string, def, minimum int) int {
	v := l.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minimum {
		l.fail(key, fmt.Errorf("invalid number %q (minimum %d)", v, minimum))
		return def
	}
	return n
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v := l.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		l.fail(key, fmt.Errorf("invalid duration %q", v))
		return def
	}
	return d
}

func (l *loader) percent(key string, def float64) float64 {
	v := l.str(key, "")
	if v == "" {
		return def
	}
	p, err := parsePercent(v)
	if err != nil {
		l.fail(key, err)
		return def
	}
	return p
}

// list splits a colon separated list, dropping empty items.
func (l *loader) list(key string, def []string) []string {
	var out []string
	for item := range strings.SplitSeq(l.env[key], ":") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// script returns the path of a configured script and checks that it exists
// and is executable. 1.x silently ignored a missing script, which hid broken
// hooks; this is an error now.
func (l *loader) script(key string) string {
	path := l.str(key, "")
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		l.fail(key, fmt.Errorf("script %q: %w", path, errors.Unwrap(err)))
	case fi.IsDir():
		l.fail(key, fmt.Errorf("%q is a directory", path))
	case fi.Mode()&0o111 == 0:
		l.fail(key, fmt.Errorf("script %q is not executable", path))
	}
	return path
}

// repository composes the repository from RESTIC_REPOSITORY or from
// RESTIC_REPOSITORY_BASE and RESTIC_REPOSITORY_PATH.
func (l *loader) repository() string {
	if repo := l.str("RESTIC_REPOSITORY", ""); repo != "" {
		return repo
	}
	base, path := l.str("RESTIC_REPOSITORY_BASE", ""), l.str("RESTIC_REPOSITORY_PATH", "")
	if base == "" || path == "" {
		return ""
	}
	repo := strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
	l.env["RESTIC_REPOSITORY"] = repo
	return repo
}

// requirePassword checks that restic can obtain a password for prefix
// (RESTIC_PASSWORD, ...), either directly, from a file or from a command.
func (l *loader) requirePassword(prefix string) {
	n := 0
	for _, suffix := range []string{"", "_FILE", "_COMMAND"} {
		if l.str(prefix+suffix, "") != "" {
			n++
		}
	}
	if n == 0 {
		l.fail(prefix, errors.New("not set (alternatively set "+prefix+"_FILE or "+prefix+"_COMMAND)"))
	}
}

func (l *loader) copyConfig() *Copy {
	repo := l.str("COPY_REPOSITORY", "")
	if repo == "" {
		for _, k := range []string{"COPY_PASSWORD", "COPY_PASSWORD_FILE", "COPY_APPEND_ONLY"} {
			if l.str(k, "") != "" {
				l.warn(k + " is set but COPY_REPOSITORY is not: ignored")
			}
		}
		return nil
	}
	cp := &Copy{
		Repository:   repo,
		Password:     l.env["COPY_PASSWORD"],
		PasswordFile: l.str("COPY_PASSWORD_FILE", ""),
		AppendOnly:   l.boolean("COPY_APPEND_ONLY", false),
	}
	switch {
	case cp.Password == "" && cp.PasswordFile == "":
		l.fail("COPY_PASSWORD", errors.New("COPY_REPOSITORY needs COPY_PASSWORD or COPY_PASSWORD_FILE"))
	case cp.Password != "" && cp.PasswordFile != "":
		l.fail("COPY_PASSWORD", errors.New("set COPY_PASSWORD or COPY_PASSWORD_FILE, not both"))
	}
	if repo == l.str("RESTIC_REPOSITORY", "") {
		l.fail("COPY_REPOSITORY", errors.New("must differ from RESTIC_REPOSITORY"))
	}
	return cp
}

func (l *loader) root() string {
	root := l.str("BACKUP_ROOT", "")
	if root == "" {
		l.fail("BACKUP_ROOT", errors.New("not set"))
		return ""
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		l.fail("BACKUP_ROOT", err)
		return root
	}
	if fi, err := os.Stat(abs); err != nil {
		l.fail("BACKUP_ROOT", fmt.Errorf("%q: %w", abs, errors.Unwrap(err)))
	} else if !fi.IsDir() {
		l.fail("BACKUP_ROOT", fmt.Errorf("%q is not a directory", abs))
	}
	return abs
}

// retention parses RETENTION_POLICY, which is required unless the repository is
// append-only (nothing is ever forgotten then).
func (l *loader) retention(appendOnly bool) Retention {
	v := l.str("RETENTION_POLICY", "")
	if v == "" {
		if !appendOnly {
			l.fail("RETENTION_POLICY", errors.New(`not set, e.g. {"weekly":8,"daily":7,"last":10}`))
		}
		return Retention{}
	}
	r, err := ParseRetention(v)
	if err != nil {
		l.fail("RETENTION_POLICY", err)
	}
	return r
}

// schedule validates the cron expression in key. A variable that is set but
// empty disables the schedule.
func (l *loader) schedule(key, def string, loc *time.Location) string {
	expr, set := l.env[key]
	if !set {
		expr = def
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	if _, err := parseSchedule(expr, loc); err != nil {
		l.fail(key, err)
	}
	return expr
}

// scheduleFile handles CRON_SCHEDULE_FILE. The file may appear later, so a
// missing file is only a warning; an existing one must be valid.
func (l *loader) scheduleFile(envExpr string, loc *time.Location) string {
	path := l.str("CRON_SCHEDULE_FILE", "")
	if path == "" {
		return ""
	}
	if envExpr != "" {
		l.fail("CRON_SCHEDULE_FILE", errors.New("set CRON_SCHEDULE or CRON_SCHEDULE_FILE, not both"))
		return path
	}
	data, err := os.ReadFile(path) //nolint:gosec // the operator chooses the schedule file
	switch {
	case errors.Is(err, os.ErrNotExist):
		l.warn(fmt.Sprintf("CRON_SCHEDULE_FILE %q not found: no backups until it exists", path))
	case err != nil:
		l.fail("CRON_SCHEDULE_FILE", err)
	default:
		if _, err := parseSchedule(string(data), loc); err != nil {
			l.fail("CRON_SCHEDULE_FILE", fmt.Errorf("%q: %w", path, err))
		}
	}
	return path
}

// pruneInterval maps the 1.x BACKUP_PRUNE_INTERVAL: 0 turned pruning off, any
// other value is replaced by PRUNE_SCHEDULE.
func (l *loader) pruneInterval(c *Config) {
	v := l.str("BACKUP_PRUNE_INTERVAL", "")
	if v == "" {
		return
	}
	d, err := time.ParseDuration(v)
	switch {
	case err != nil:
		l.warn(fmt.Sprintf("BACKUP_PRUNE_INTERVAL %q is invalid and ignored", v))
	case d == 0 && !l.isSet("PRUNE_SCHEDULE"):
		c.Schedule.Prune = ""
	case d == 0:
		l.warn("BACKUP_PRUNE_INTERVAL=0 ignored because PRUNE_SCHEDULE is set")
	default:
		l.warn("BACKUP_PRUNE_INTERVAL is deprecated: use PRUNE_SCHEDULE")
	}
}

func (l *loader) isSet(key string) bool { _, ok := l.env[key]; return ok }

func (l *loader) verify(c *Config) Verify {
	v := Verify{
		Mode:        l.str("VERIFY_MODE", "sample"),
		SampleFiles: l.integer("VERIFY_SAMPLE_FILES", 100, 1),
		SampleBytes: 256 << 20,
		Dir:         l.str("VERIFY_DIR", ""),
		Script:      l.script("VERIFY_SCRIPT"),
		Copy:        l.boolean("VERIFY_COPY", false),
	}
	if v.Mode != "sample" && v.Mode != "full" {
		l.fail("VERIFY_MODE", fmt.Errorf("%q is not one of sample, full", v.Mode))
	}
	if s := l.str("VERIFY_SAMPLE_BYTES", ""); s != "" {
		n, err := parseSize(s)
		if err != nil || n <= 0 {
			l.fail("VERIFY_SAMPLE_BYTES", fmt.Errorf("invalid size %q", s))
		} else {
			v.SampleBytes = n
		}
	}
	if v.Copy && c.Copy == nil {
		l.fail("VERIFY_COPY", errors.New("needs COPY_REPOSITORY"))
	}
	// The sandbox is deleted after the test and must never touch live data.
	dir := v.Dir
	if dir == "" {
		dir = os.TempDir()
	}
	if c.Root != "" && within(c.Root, dir) {
		l.fail("VERIFY_DIR", fmt.Errorf("%q must not be inside BACKUP_ROOT %q", dir, c.Root))
	}
	return v
}

// within reports whether path is dir itself or lies below it.
func within(dir, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (l *loader) notify() Notify {
	n := Notify{
		Command: l.str("NOTIFY_COMMAND", ""),
		On:      []string{"failure", "warning", "recovered"},
		Timeout: l.duration("NOTIFY_TIMEOUT", 30*time.Second),
	}
	if v := l.str("NOTIFY_ON", ""); v != "" {
		n.On = nil
		for item := range strings.SplitSeq(v, ",") {
			item = strings.ToLower(strings.TrimSpace(item))
			if !slices.Contains([]string{"failure", "warning", "recovered", "success", "all"}, item) {
				l.fail("NOTIFY_ON", fmt.Errorf("%q is not one of failure, warning, recovered, success, all", item))
				continue
			}
			n.On = append(n.On, item)
		}
	}
	return n
}

func (l *loader) server() Server {
	s := Server{
		HTTPPort: l.integer("BACKUP_HTTP_PORT", 9000, 1),
		Socket:   l.str("MCS_BACKUP_SOCKET", DefaultSocket()),
		StateDir: l.str("MCS_BACKUP_STATE_DIR", filepath.Join(os.TempDir(), "mcs-backup")),
		WebAddr:  l.str("MCS_BACKUP_WEB_ADDR", ""),
		WebToken: l.env["MCS_BACKUP_WEB_TOKEN"],
	}
	if s.HTTPPort > 65535 {
		l.fail("BACKUP_HTTP_PORT", fmt.Errorf("port %d out of range", s.HTTPPort))
	}
	if s.WebAddr != "" {
		if _, port, err := net.SplitHostPort(s.WebAddr); err != nil {
			l.fail("MCS_BACKUP_WEB_ADDR", err)
		} else if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			l.fail("MCS_BACKUP_WEB_ADDR", fmt.Errorf("invalid port %q", port))
		}
	}
	return s
}

func (l *loader) resticArgs() ResticArgs {
	a := ResticArgs{
		Bin:     l.str("MCS_BACKUP_RESTIC", "restic"),
		Backup:  l.args("RESTIC_BACKUP_ARGS", OpBackup),
		Forget:  l.args("RESTIC_FORGET_ARGS", OpForget),
		Prune:   l.args("RESTIC_PRUNE_ARGS", OpPrune),
		Check:   l.args("RESTIC_CHECK_ARGS", OpCheck),
		Restore: l.args("RESTIC_RESTORE_ARGS", OpRestore),
	}
	if l.boolean("S3_USE_V1", false) {
		a.Global = append(a.Global, "-o", "s3.list-objects-v1=true")
	}
	return a
}

func (l *loader) args(key, op string) []string {
	args, err := SplitArgs(l.env[key])
	if err == nil {
		err = ValidateArgs(op, args)
	}
	if err != nil {
		l.fail(key, err)
		return nil
	}
	return args
}

// metricsLabels parses METRICS_LABELS, a YAML or JSON mapping. Variables are
// expanded in the value, all label values become strings.
func (l *loader) metricsLabels() map[string]string {
	raw := l.str("METRICS_LABELS", "")
	if raw == "" {
		return nil
	}
	m, err := parseLabels(os.Expand(raw, func(k string) string { return l.env[k] }))
	if err != nil {
		l.fail("METRICS_LABELS", err)
	}
	return m
}
