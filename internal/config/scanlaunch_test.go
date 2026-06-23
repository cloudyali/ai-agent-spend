package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadScanOnLaunch governs whether a read command brings the ledger current before
// rendering. It defaults to ON (the onboarding win: install → run → value) and is
// turned off with `scan_on_launch = false`, so an explicit `aispend scan` is required.
func TestLoadScanOnLaunch(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" means no config.toml at all
		want    bool
		wantErr bool
	}{
		{"absent file defaults on", "", true, false},
		{"blank value defaults on", "scan_on_launch =\n", true, false},
		{"explicit false", "scan_on_launch = false\n", false, false},
		{"explicit true", "scan_on_launch = true\n", true, false},
		{"zero is off", "scan_on_launch = 0\n", false, false},
		{"one is on", "scan_on_launch = 1\n", true, false},
		{"no/off is off", "scan_on_launch = no\n", false, false},
		{"case-insensitive False", "scan_on_launch = False\n", false, false},
		{"unrelated keys ignored", "plan = \"claude-max-20x\"\n", true, false},
		{"garbage value errors, defaults on", "scan_on_launch = maybe\n", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadScanOnLaunch(home)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("LoadScanOnLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}
