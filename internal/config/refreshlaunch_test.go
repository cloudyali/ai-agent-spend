package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadRefreshOnLaunch governs whether a read command tops up a stale (>24h) LiteLLM
// price cache before pricing. It defaults to ON (keep prices fresh automatically) and
// is turned off with `refresh_on_launch = false`, mirroring scan_on_launch.
func TestLoadRefreshOnLaunch(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" means no config.toml at all
		want    bool
		wantErr bool
	}{
		{"absent file defaults on", "", true, false},
		{"blank value defaults on", "refresh_on_launch =\n", true, false},
		{"explicit false", "refresh_on_launch = false\n", false, false},
		{"explicit true", "refresh_on_launch = true\n", true, false},
		{"zero is off", "refresh_on_launch = 0\n", false, false},
		{"one is on", "refresh_on_launch = 1\n", true, false},
		{"no/off is off", "refresh_on_launch = off\n", false, false},
		{"case-insensitive False", "refresh_on_launch = False\n", false, false},
		{"unrelated keys ignored", "plan = \"claude-max-20x\"\n", true, false},
		{"garbage value errors, defaults on", "refresh_on_launch = maybe\n", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadRefreshOnLaunch(home)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("LoadRefreshOnLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}
