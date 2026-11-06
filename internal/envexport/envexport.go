// Package envexport describes what is needed to run restic against the
// repository of an instance elsewhere: the environment variables, restic
// options and hints about credentials that live outside of the environment.
package envexport

import (
	"maps"
	"slices"
	"strings"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/redact"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Mask replaces secret values unless they are revealed.
const Mask = redact.Mask

// Build describes the restic access in env (KEY=VALUE pairs of the restic
// environment), args (global restic options) and the oldest supported restic
// version. Secrets are masked unless reveal is set.
func Build(env, args []string, minVersion string, reveal bool) service.EnvInfo {
	vars := parse(env)
	scheme := schemeOf(vars["RESTIC_REPOSITORY"])
	if scheme == "" && vars["RESTIC_REPOSITORY"] == "" && vars["RESTIC_REPOSITORY_FILE"] != "" {
		scheme = "?" // unknown: the repository is in a file
	}

	info := service.EnvInfo{MinVersion: minVersion}
	for _, name := range selected(vars, scheme) {
		info.Vars = append(info.Vars, variable(name, vars[name], reveal))
	}
	if len(args) > 0 {
		info.Args = slices.Clone(args)
		info.Notes = append(info.Notes, "Pass "+strings.Join(args, " ")+" to restic every time: these options have no environment variable.")
	}
	info.Notes = append(info.Notes, notes(vars, scheme)...)
	return info
}

func parse(env []string) map[string]string {
	vars := make(map[string]string, len(env))
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok {
			vars[name] = value
		}
	}
	return vars
}

// schemeOf returns the backend of a repository: "s3", "b2", "azure", "gs",
// "swift", "rest", "sftp", "rclone" or "local".
func schemeOf(repo string) string {
	if repo == "" {
		return ""
	}
	if scheme, _, ok := strings.Cut(repo, ":"); ok {
		switch scheme {
		case "s3", "b2", "azure", "gs", "swift", "rest", "sftp", "rclone":
			return scheme
		}
	}
	return "local"
}

// backendPrefixes are the variable prefixes each backend reads. restic
// documents them in "Preparing a new repository".
var backendPrefixes = map[string][]string{
	"s3":     {"AWS_"},
	"b2":     {"B2_"},
	"azure":  {"AZURE_"},
	"gs":     {"GOOGLE_"},
	"swift":  {"OS_", "ST_"},
	"rclone": {"RCLONE_"},
}

// proxyVars are read by every network backend.
var proxyVars = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// mcsOnly are RESTIC_* variables that configure mcs-backup, not restic.
var mcsOnly = []string{
	"RESTIC_BACKUP_ARGS", "RESTIC_FORGET_ARGS", "RESTIC_PRUNE_ARGS", "RESTIC_CHECK_ARGS", "RESTIC_RESTORE_ARGS",
	"RESTIC_REPOSITORY_BASE", "RESTIC_REPOSITORY_PATH",
}

// leading are listed first, in this order.
var leading = []string{
	"RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE",
	"RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND",
}

func selected(vars map[string]string, scheme string) []string {
	var rest []string
	for name := range vars {
		if slices.Contains(leading, name) || slices.Contains(mcsOnly, name) {
			continue
		}
		if strings.HasPrefix(name, "RESTIC_") || slices.Contains(proxyVars, name) || backendVar(name, scheme) {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	var names []string
	for _, name := range leading {
		if _, ok := vars[name]; ok {
			names = append(names, name)
		}
	}
	return append(names, rest...)
}

func backendVar(name, scheme string) bool {
	if scheme == "?" { // unknown backend: every backend's variables
		for _, prefixes := range backendPrefixes {
			if slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
				return true
			}
		}
		return false
	}
	return slices.ContainsFunc(backendPrefixes[scheme], func(p string) bool { return strings.HasPrefix(name, p) })
}

func variable(name, value string, reveal bool) service.EnvVar {
	if name == "RESTIC_REPOSITORY" {
		masked := config.MaskURL(value)
		v := service.EnvVar{Name: name, Value: value, Secret: masked != value}
		if v.Secret && !reveal {
			v.Value = masked
		}
		return v
	}
	v := service.EnvVar{Name: name, Value: value, Secret: redact.Secret(name, value)}
	if v.Secret && !reveal {
		v.Value = Mask
	}
	return v
}

// fileVars name files whose content restic reads.
var fileVars = []string{"GOOGLE_APPLICATION_CREDENTIALS", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE"}

func notes(vars map[string]string, scheme string) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if name == "RESTIC_REPOSITORY_FILE" || name == "RESTIC_PASSWORD_FILE" || slices.Contains(fileVars, name) || (strings.HasPrefix(name, "RESTIC_") && strings.HasSuffix(name, "_FILE")) {
			out = append(out, name+" names the file "+vars[name]+": it has to exist, with the same content, where restic runs.")
		}
	}
	if vars["RESTIC_PASSWORD_COMMAND"] != "" {
		out = append(out, "RESTIC_PASSWORD_COMMAND is run by restic: the command has to work where restic runs.")
	}

	has := func(names ...string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return vars[n] != "" })
	}
	switch scheme {
	case "s3":
		if !has("AWS_ACCESS_KEY_ID", "AWS_PROFILE", "AWS_SHARED_CREDENTIALS_FILE") {
			out = append(out, "No S3 credentials are set: if this instance gets them from an IAM role or a workload identity, they are not part of this export. Use an access key or run restic where the role is available.")
		}
	case "gs":
		if !has("GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_ACCESS_TOKEN") {
			out = append(out, "No Google credentials are set: if this instance uses a workload identity, it is not part of this export.")
		}
	case "azure":
		if !has("AZURE_ACCOUNT_KEY", "AZURE_ACCOUNT_SAS") {
			out = append(out, "No Azure key or SAS token is set: if this instance uses a managed identity, it is not part of this export.")
		}
	case "sftp":
		out = append(out, "The SFTP backend uses the SSH configuration, keys and known hosts of the user who runs restic.")
	case "local":
		out = append(out, "The repository is a local path: it has to be reachable under the same path where restic runs.")
	}
	return out
}
