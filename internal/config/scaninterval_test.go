package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// LoadScanInterval governs the cadence of the background `aispend daemon` scan loop.
// It defaults to 15 minutes and accepts any Go duration string (15m, 30m, 1h, 90s).
// A non-positive or malformed value keeps the safe default and returns an error so
// the caller can surface it — a daemon must never spin on a zero/negative interval.
func TestLoadScanInterval(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" means no config.toml at all
		want    time.Duration
		wantErr bool
	}{
		{"absent file defaults to 15m", "", DefaultScanInterval, false},
		{"blank value defaults to 15m", "scan_interval =\n", DefaultScanInterval, false},
		{"explicit 15m", "scan_interval = 15m\n", 15 * time.Minute, false},
		{"explicit 30m", "scan_interval = 30m\n", 30 * time.Minute, false},
		{"explicit 1h", "scan_interval = 1h\n", time.Hour, false},
		{"seconds granularity", "scan_interval = 90s\n", 90 * time.Second, false},
		{"quoted value", "scan_interval = \"45m\"\n", 45 * time.Minute, false},
		{"unrelated keys ignored", "plan = \"claude-max-20x\"\n", DefaultScanInterval, false},
		{"missing unit errors, defaults 15m", "scan_interval = 15\n", DefaultScanInterval, true},
		{"garbage errors, defaults 15m", "scan_interval = soon\n", DefaultScanInterval, true},
		{"zero rejected, defaults 15m", "scan_interval = 0\n", DefaultScanInterval, true},
		{"zero seconds rejected", "scan_interval = 0s\n", DefaultScanInterval, true},
		{"negative rejected", "scan_interval = -5m\n", DefaultScanInterval, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tc.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadScanInterval(home)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("LoadScanInterval = %v, want %v", got, tc.want)
			}
		})
	}
}
