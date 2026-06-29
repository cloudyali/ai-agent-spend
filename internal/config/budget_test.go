package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBudget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("budget_usd = 500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	micros, ok, err := LoadBudget(dir)
	if err != nil || !ok || micros != 500_000_000 {
		t.Fatalf("LoadBudget = %d, %v, %v; want 500000000, true, nil", micros, ok, err)
	}

	if _, ok, _ := LoadBudget(t.TempDir()); ok { // no config file → off
		t.Error("absent budget_usd should be off (ok=false)")
	}

	d2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(d2, "config.toml"), []byte("budget_usd = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadBudget(d2); ok { // non-positive → off
		t.Error("budget_usd = 0 should be off")
	}
}
