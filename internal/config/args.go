package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// SplitArgs splits s into arguments using shell-like quoting: whitespace
// separates arguments, single quotes are literal, double quotes allow \" and
// \\, and a backslash outside quotes escapes the next character. There is no
// variable or glob expansion.
func SplitArgs(s string) ([]string, error) {
	var (
		args  []string
		cur   strings.Builder
		inArg bool
		quote rune
	)
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\'):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inArg = true
		case r == '\\':
			if i+1 == len(runes) {
				return nil, errors.New("trailing backslash")
			}
			i++
			cur.WriteRune(runes[i])
			inArg = true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

// JoinArgs is the inverse of SplitArgs.
func JoinArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteArg(a)
	}
	return strings.Join(quoted, " ")
}

func quoteArg(a string) string {
	if a == "" {
		return "''"
	}
	safe := !strings.ContainsFunc(a, func(r rune) bool {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		return !alnum && !strings.ContainsRune("_@%+=:,./-", r)
	})
	if safe {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// Restic operations that accept additional arguments from the configuration.
const (
	OpBackup  = "backup"
	OpForget  = "forget"
	OpPrune   = "prune"
	OpCheck   = "check"
	OpRestore = "restore"
)

// rejectedFlags lists the flags mcs-backup sets itself for op. Allowing them in
// the extra arguments would silently change the behavior mcs-backup relies on.
func rejectedFlags(op string) []string {
	common := []string{
		"--json", "-r", "--repo", "--repository-file", "--password-file", "--password-command",
		"--from-repo", "--from-repository-file", "--from-password-file", "--from-password-command",
		"--no-lock",
	}
	switch op {
	case OpBackup:
		return append(common, "--host", "-H", "--tag", "--parent", "--stdin", "--stdin-from-command",
			"--stdin-filename", "--dry-run", "-n", "--files-from", "--files-from-raw", "--files-from-verbatim")
	case OpForget:
		return append(common, "--host", "--tag", "--group-by", "-g", "--dry-run", "-n", "--prune",
			"--unsafe-allow-remove-all")
	case OpPrune:
		return append(common, "--dry-run", "-n")
	case OpRestore:
		return append(common, "--target", "-t", "--delete", "--dry-run", "-n", "--verify",
			"--include", "-i", "--iinclude", "--include-file", "--iinclude-file", "--host", "-H", "--tag")
	default:
		return common
	}
}

// ValidateArgs rejects extra arguments for op that collide with flags set by
// mcs-backup itself.
func ValidateArgs(op string, args []string) error {
	rejected := rejectedFlags(op)
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if name, ok := rejectedFlag(arg, rejected); ok {
			return fmt.Errorf("flag %s is set by mcs-backup and cannot be used with restic %s", name, op)
		}
	}
	return nil
}

func rejectedFlag(arg string, rejected []string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(arg, "=")
	if slices.Contains(rejected, name) {
		return name, true
	}
	// a short flag with an attached value, e.g. -rrepo
	if !strings.HasPrefix(arg, "--") && len(arg) > 2 && slices.Contains(rejected, arg[:2]) {
		return arg[:2], true
	}
	return "", false
}
