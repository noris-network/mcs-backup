package config

import "testing"

func TestParsePercent(t *testing.T) {
	tests := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"50", 50, false},
		{"50%", 50, false},
		{" 12.5 % ", 12.5, false},
		{"0", 0, false},
		{"100", 100, false},
		{"101", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parsePercent(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parsePercent(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"1048576", 1048576, false},
		{"256MiB", 256 << 20, false},
		{"256 mib", 256 << 20, false},
		{"1G", 1 << 30, false},
		{"1.5GB", 1_500_000_000, false},
		{"10kb", 10_000, false},
		{"2TiB", 2 << 40, false},
		{"12 parsecs", 0, true},
		{"MiB", 0, true},
		{"-5", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := parseSize(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseSize(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
