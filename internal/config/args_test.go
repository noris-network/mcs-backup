package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", nil, false},
		{"   ", nil, false},
		{"--limit-upload 1000", []string{"--limit-upload", "1000"}, false},
		{`--exclude "a b" --tag 'x y'`, []string{"--exclude", "a b", "--tag", "x y"}, false},
		{`a\ b c`, []string{"a b", "c"}, false},
		{`"say \"hi\" \\ \n"`, []string{`say "hi" \ \n`}, false},
		{`''`, []string{""}, false},
		{`a'b c'd`, []string{"ab cd"}, false},
		{"a\tb\nc", []string{"a", "b", "c"}, false},
		{`"open`, nil, true},
		{`'open`, nil, true},
		{`trailing\`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := SplitArgs(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SplitArgs(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitArgs(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestJoinArgsRoundTrip(t *testing.T) {
	cases := [][]string{
		{"--exclude", "a b"},
		{"it's", `say "hi"`, "", "plain", `back\slash`, "$HOME", "a:b,c=d"},
		{"--one-file-system"},
	}
	for _, args := range cases {
		joined := JoinArgs(args)
		got, err := SplitArgs(joined)
		if err != nil {
			t.Fatalf("SplitArgs(%q): %v", joined, err)
		}
		if !reflect.DeepEqual(got, args) {
			t.Errorf("round trip of %q via %q = %q", args, joined, got)
		}
	}
}

func TestValidateArgs(t *testing.T) {
	tests := []struct {
		op      string
		args    []string
		wantErr string
	}{
		{OpBackup, []string{"--one-file-system", "--exclude-caches", "--limit-upload=100"}, ""},
		{OpBackup, []string{"--host", "x"}, "--host"},
		{OpBackup, []string{"--host=x"}, "--host"},
		{OpBackup, []string{"--json"}, "--json"},
		{OpBackup, []string{"-rfoo"}, "-r"},
		{OpBackup, []string{"--tag", "x"}, "--tag"},
		{OpBackup, []string{"--", "--host"}, ""},
		{OpForget, []string{"--keep-within", "30d"}, ""},
		{OpForget, []string{"--group-by", "host"}, "--group-by"},
		{OpForget, []string{"-n"}, "-n"},
		{OpForget, []string{"--prune"}, "--prune"},
		{OpPrune, []string{"--max-unused", "5%"}, ""},
		{OpPrune, []string{"--dry-run"}, "--dry-run"},
		{OpCheck, []string{"--read-data-subset=2%"}, ""},
		{OpCheck, []string{"--password-file", "x"}, "--password-file"},
		{OpRestore, []string{"--sparse"}, ""},
		{OpRestore, []string{"--target", "/x"}, "--target"},
		{OpRestore, []string{"--delete"}, "--delete"},
		{OpRestore, []string{"--include-file=x"}, "--include-file"},
	}
	for _, tt := range tests {
		t.Run(tt.op+" "+strings.Join(tt.args, " "), func(t *testing.T) {
			err := ValidateArgs(tt.op, tt.args)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}
