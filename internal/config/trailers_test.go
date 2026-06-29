package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAispendTOML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".aispend.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTrailers_DefaultsWhenAbsent(t *testing.T) {
	got, err := LoadTrailers(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultTrailers() {
		t.Errorf("absent config = %+v, want %+v", got, DefaultTrailers())
	}
	if !got.Enabled || !got.Cost || got.Precision != 2 || got.CostName != "AI-Cost" {
		t.Errorf("defaults wrong: %+v", got)
	}
}

func TestLoadTrailers_ParsesSection(t *testing.T) {
	dir := t.TempDir()
	writeAispendTOML(t, dir, `project = "payments"

[trailers]
cost = true
cost_models = true
tokens = true
interactions = true
precision = 3

[trailers.rename]
cost = "Claude-Cost-Equiv"
`)
	got, err := LoadTrailers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CostModels || !got.Tokens || !got.Interactions {
		t.Errorf("section bools not parsed: %+v", got)
	}
	if got.Precision != 3 {
		t.Errorf("precision = %d, want 3", got.Precision)
	}
	if got.CostName != "Claude-Cost-Equiv" {
		t.Errorf("rename cost = %q, want Claude-Cost-Equiv", got.CostName)
	}
	// A [trailers] section must not break the existing flat LoadRepo reader.
	if r, ok, _ := LoadRepo(dir); !ok || r.Project != "payments" {
		t.Errorf("LoadRepo regressed with a [trailers] section: %+v ok=%v", r, ok)
	}
}

func TestLoadTrailers_EnabledFalseAndPrecisionClamp(t *testing.T) {
	dir := t.TempDir()
	writeAispendTOML(t, dir, "[trailers]\nenabled = false\nprecision = 99\n")
	got, err := LoadTrailers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Error("enabled = false must disable trailers repo-wide")
	}
	if got.Precision != 8 {
		t.Errorf("precision = %d, want clamped to 8", got.Precision)
	}
	if !got.Cost {
		t.Error("cost must stay default-on when only enabled/precision are set")
	}
}

func TestLoadTrailers_CostFalse(t *testing.T) {
	dir := t.TempDir()
	writeAispendTOML(t, dir, "[trailers]\ncost = false\n")
	got, _ := LoadTrailers(dir)
	if got.Cost {
		t.Error("cost = false must turn the cost line off")
	}
}

func TestLoadTrailers_EmptyRenameKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	writeAispendTOML(t, dir, "[trailers.rename]\ncost = \"\"\n")
	got, _ := LoadTrailers(dir)
	if got.CostName != "AI-Cost" {
		t.Errorf("empty rename must keep the default name, got %q", got.CostName)
	}
}
