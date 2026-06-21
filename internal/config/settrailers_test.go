package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetTrailers_RoundTripsAndPreserves(t *testing.T) {
	dir := t.TempDir()
	// Unrelated config must survive a [trailers] write.
	writeAispendTOML(t, dir, "project = \"payments\"\ncost_tag = \"api\"\n")

	want := Trailers{Enabled: true, Cost: true, CostModels: true, Tokens: false, Interactions: true, Precision: 3, CostName: "Claude-Cost-Equiv"}
	if err := SetTrailers(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTrailers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
	if r, ok, _ := LoadRepo(dir); !ok || r.Project != "payments" || r.CostTag != "api" {
		t.Errorf("SetTrailers must preserve other config, got %+v ok=%v", r, ok)
	}

	// Re-save (changed field) must overwrite in place, not duplicate the section.
	want.Cost = false
	if err := SetTrailers(dir, want); err != nil {
		t.Fatal(err)
	}
	if g, _ := LoadTrailers(dir); g.Cost {
		t.Error("overwrite should set cost=false")
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".aispend.toml"))
	if n := strings.Count(string(b), "[trailers]"); n != 1 {
		t.Errorf("must not duplicate [trailers] (found %d):\n%s", n, b)
	}
}

func TestSetTrailers_DefaultNameWritesNoRename(t *testing.T) {
	dir := t.TempDir()
	if err := SetTrailers(dir, DefaultTrailers()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".aispend.toml"))
	if strings.Contains(string(b), "[trailers.rename]") {
		t.Errorf("the default cost name should not emit a rename section:\n%s", b)
	}
}
