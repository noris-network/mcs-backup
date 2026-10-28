package envexport

import (
	"fmt"
	"io"
	"strings"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Style is an output format.
type Style string

// The supported output formats.
const (
	StyleExport Style = "export" // export NAME='value' lines for a POSIX shell
	StyleDotenv Style = "dotenv" // NAME=value lines for an .env file
	StyleDocker Style = "docker" // a docker run command with the restic image
)

// Styles lists the formats for help texts.
func Styles() []Style { return []Style{StyleExport, StyleDotenv, StyleDocker} }

// ParseStyle parses the name of a format.
func ParseStyle(s string) (Style, error) {
	for _, st := range Styles() {
		if string(st) == s {
			return st, nil
		}
	}
	return "", fmt.Errorf("unknown format %q (use export, dotenv or docker)", s)
}

// Write writes info in the given style. Notes become comments, so the
// output of the export and dotenv formats can be used as it is.
func Write(w io.Writer, info service.EnvInfo, style Style) error {
	var b strings.Builder
	switch style {
	case StyleExport:
		comments(&b, info)
		for _, v := range info.Vars {
			fmt.Fprintf(&b, "export %s=%s\n", v.Name, shellQuote(v.Value))
		}
	case StyleDotenv:
		comments(&b, info)
		for _, v := range info.Vars {
			fmt.Fprintf(&b, "%s=%s\n", v.Name, dotenvValue(v.Value))
		}
	case StyleDocker:
		comments(&b, info)
		b.WriteString("docker run --rm -it \\\n")
		for _, v := range info.Vars {
			fmt.Fprintf(&b, "  -e %s=%s \\\n", v.Name, shellQuote(v.Value))
		}
		b.WriteString("  restic/restic")
		for _, a := range info.Args {
			b.WriteString(" " + shellWord(a))
		}
		b.WriteString("\n")
	default:
		return fmt.Errorf("unknown format %q", style)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func comments(b *strings.Builder, info service.EnvInfo) {
	fmt.Fprintf(b, "# restic %s or newer is needed\n", info.MinVersion)
	for _, n := range info.Notes {
		fmt.Fprintf(b, "# %s\n", n)
	}
}

// shellWord quotes s only if the shell would otherwise change it.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return s
	}
	return shellQuote(s)
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// dotenvValue leaves plain values alone and double-quotes the others. Docker's
// --env-file does not remove quotes; the docker format is for that.
func dotenvValue(s string) string {
	if !strings.ContainsAny(s, " \t\n\r#\"'$\\`") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "\n", `\n`, "\r", `\r`).Replace(s) + `"`
}
