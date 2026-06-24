package pricesync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tbl builds a LiteLLM-schema table from model -> input cost/token (output = 2x).
func tbl(t *testing.T, models map[string]float64) []byte {
	t.Helper()
	m := map[string]map[string]float64{}
	for name, in := range models {
		m[name] = map[string]float64{
			"input_cost_per_token":  in,
			"output_cost_per_token": in * 2,
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal table: %v", err)
	}
	return b
}

func baseModels() map[string]float64 {
	return map[string]float64{
		"gpt-4o":            0.0000025,
		"gpt-3.5-turbo":     0.0000005,
		"claude-opus-4-8":   0.000015,
		"claude-sonnet-4-6": 0.000003,
		"extra-1":           0.000001,
		"extra-2":           0.000001,
		"extra-3":           0.000001,
	}
}

func testCfg() Config {
	return Config{
		MinModels:       5,
		MaxDropFraction: 0.20,
		MaxSwingFactor:  10,
		MaxSwingModels:  2, // 3+ non-anchor swings in one sync = systemic
		RequiredModels:  []string{"gpt-4o", "claude-opus-4-8"},
	}
}

func violationsJoined(r Report) string { return strings.Join(r.Violations, " | ") }

func TestValidate_AcceptsHealthyUpdate(t *testing.T) {
	rep, err := Validate(tbl(t, baseModels()), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK, got violations: %s", violationsJoined(rep))
	}
	if rep.CurrentModels != 7 || rep.PreviousModels != 7 {
		t.Errorf("models = %d/%d, want 7/7", rep.CurrentModels, rep.PreviousModels)
	}
}

func TestValidate_RejectsUnparseableCurrent(t *testing.T) {
	if _, err := Validate([]byte("{not json"), nil, testCfg()); err == nil {
		t.Fatal("expected an error for unparseable current table")
	}
}

func TestValidate_RejectsBelowFloor(t *testing.T) {
	curr := tbl(t, map[string]float64{"gpt-4o": 0.0000025, "claude-opus-4-8": 0.000015})
	rep, err := Validate(curr, nil, testCfg()) // MinModels = 5
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(violationsJoined(rep), "floor") {
		t.Errorf("expected a floor violation, got: %s", violationsJoined(rep))
	}
}

func TestValidate_RejectsCountDrop(t *testing.T) {
	prev := tbl(t, baseModels()) // 7 models
	shrunk := map[string]float64{
		"gpt-4o":            0.0000025,
		"claude-opus-4-8":   0.000015,
		"claude-sonnet-4-6": 0.000003,
		"gpt-3.5-turbo":     0.0000005,
	} // 4 models -> 43% drop
	cfg := Config{MinModels: 1, MaxDropFraction: 0.20, MaxSwingFactor: 10, RequiredModels: []string{"gpt-4o"}}
	rep, err := Validate(tbl(t, shrunk), prev, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(strings.ToLower(violationsJoined(rep)), "drop") {
		t.Errorf("expected a drop violation, got: %s", violationsJoined(rep))
	}
}

func TestValidate_RejectsMissingAnchor(t *testing.T) {
	m := baseModels()
	delete(m, "gpt-4o")
	rep, err := Validate(tbl(t, m), nil, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(violationsJoined(rep), "gpt-4o") {
		t.Errorf("expected gpt-4o anchor violation, got: %s", violationsJoined(rep))
	}
}

func TestValidate_RejectsPriceCollapseToZero(t *testing.T) {
	m := baseModels()
	m["gpt-4o"] = 0
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(violationsJoined(rep), "gpt-4o") {
		t.Errorf("expected gpt-4o violation, got: %s", violationsJoined(rep))
	}
}

func warningsJoined(r Report) string { return strings.Join(r.Warnings, " | ") }

// A lone out-of-band swing on a non-anchor model is a warning that still
// publishes — it must NOT wedge the pipeline (the amazon.titan-embed regression).
func TestValidate_LoneSwingWarnsAndPublishes(t *testing.T) {
	m := baseModels()
	m["extra-1"] = 0.00005 // 50x jump from 0.000001, non-anchor, alone
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a lone non-anchor swing must publish, got violations: %s", violationsJoined(rep))
	}
	if !strings.Contains(warningsJoined(rep), "extra-1") {
		t.Errorf("expected an extra-1 swing warning, got: %s", warningsJoined(rep))
	}
	if rep.Repriced != 1 {
		t.Errorf("Repriced = %d, want 1", rep.Repriced)
	}
}

// Swings up to (and including) the tolerance still publish.
func TestValidate_SwingsAtToleranceStillPublish(t *testing.T) {
	m := baseModels()
	m["extra-1"] = 0.00005 // 50x
	m["extra-2"] = 0.00005 // 50x — 2 swings == MaxSwingModels(2), not yet systemic
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("swings at tolerance must publish, got: %s", violationsJoined(rep))
	}
	if len(rep.Warnings) != 2 {
		t.Errorf("Warnings = %d, want 2", len(rep.Warnings))
	}
}

// A burst of swings beyond tolerance is the corrupt-table signal — hold the publish.
func TestValidate_SystemicSwingsFail(t *testing.T) {
	m := baseModels()
	m["extra-1"] = 0.00005
	m["extra-2"] = 0.00005
	m["extra-3"] = 0.00005 // 3 non-anchor swings > MaxSwingModels(2) = systemic
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(violationsJoined(rep), "systemic") {
		t.Errorf("expected a systemic-reprice violation, got: %s", violationsJoined(rep))
	}
}

// A lone non-anchor collapse-to-0 is also just a warning (same wedge class).
func TestValidate_LoneCollapseNonAnchorWarns(t *testing.T) {
	m := baseModels()
	m["extra-1"] = 0 // non-anchor model goes free, alone
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a lone non-anchor collapse must publish, got: %s", violationsJoined(rep))
	}
	if !strings.Contains(warningsJoined(rep), "extra-1") {
		t.Errorf("expected an extra-1 collapse warning, got: %s", warningsJoined(rep))
	}
}

// Upstream ids are attacker-influenceable (a compromised LiteLLM push); a crafted id
// must not smuggle terminal escape bytes into operator-visible output (CWE-150).
func TestValidate_SanitizesUpstreamModelIDs(t *testing.T) {
	const evil = "evil\x1b[2Jmodel" // ESC + clear-screen embedded in the model id
	cur, prv := baseModels(), baseModels()
	cur[evil] = 0.00005 // swings 50x vs prev → a non-anchor warning
	prv[evil] = 0.000001
	rep, err := Validate(tbl(t, cur), tbl(t, prv), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := warningsJoined(rep)
	if strings.ContainsRune(joined, '\x1b') {
		t.Errorf("ESC byte leaked into warning output: %q", joined)
	}
	if !strings.ContainsRune(joined, '�') {
		t.Errorf("expected the stripped byte to surface as U+FFFD: %q", joined)
	}
}

// Anchors are canaries the client prices on: any swing of one is a hard failure,
// even alone — never silently ship a bad price for a model that matters.
func TestValidate_AnchorSwingAlwaysFails(t *testing.T) {
	m := baseModels()
	m["gpt-4o"] = 0.000125 // 50x jump on an anchor, alone
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() || !strings.Contains(violationsJoined(rep), "gpt-4o") {
		t.Errorf("expected a gpt-4o anchor swing violation, got: %s", violationsJoined(rep))
	}
}

func TestValidate_FirstRunNoPrevSkipsDiffChecks(t *testing.T) {
	rep, err := Validate(tbl(t, baseModels()), nil, testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() || rep.PreviousModels != 0 {
		t.Fatalf("expected OK first run with 0 prev, got ok=%v prev=%d: %s", rep.OK(), rep.PreviousModels, violationsJoined(rep))
	}
}

func TestValidate_AllowsModerateRepriceAndCountsIt(t *testing.T) {
	m := baseModels()
	m["extra-1"] = 0.000002 // 2x, within band
	rep, err := Validate(tbl(t, m), tbl(t, baseModels()), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK, got: %s", violationsJoined(rep))
	}
	if rep.Repriced != 1 {
		t.Errorf("Repriced = %d, want 1", rep.Repriced)
	}
}

func TestValidate_CorruptPrevTreatedAsFirstRun(t *testing.T) {
	rep, err := Validate(tbl(t, baseModels()), []byte("{broken"), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("a corrupt previous table must not fail a healthy current; got: %s", violationsJoined(rep))
	}
}

func TestBuild_ProducesConsistentArtifacts(t *testing.T) {
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	art, err := Build(tbl(t, baseModels()), now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var table map[string]map[string]any
	if err := json.Unmarshal(art.LiteLLM, &table); err != nil {
		t.Fatalf("litellm.json not a valid object: %v", err)
	}
	if _, ok := table["gpt-4o"]; !ok {
		t.Error("expected gpt-4o present in published table")
	}
	var idx struct {
		ModelCount int               `json:"model_count"`
		Files      map[string]string `json:"files"`
	}
	if err := json.Unmarshal(art.Index, &idx); err != nil {
		t.Fatalf("index.json invalid: %v", err)
	}
	if idx.ModelCount != 7 {
		t.Errorf("ModelCount = %d, want 7", idx.ModelCount)
	}
	if idx.Files["litellm.json"] != art.SHA256 {
		t.Errorf("index sha256 = %q, want %q", idx.Files["litellm.json"], art.SHA256)
	}
}

func TestBuild_Deterministic(t *testing.T) {
	now := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	a, err := Build(tbl(t, baseModels()), now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(tbl(t, baseModels()), now)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.LiteLLM) != string(b.LiteLLM) || string(a.Index) != string(b.Index) {
		t.Error("Build output not deterministic across runs")
	}
}

func TestBuild_RejectsUnparseable(t *testing.T) {
	if _, err := Build([]byte("nope"), time.Now()); err == nil {
		t.Fatal("expected error building from unparseable table")
	}
}

func TestRun_WritesArtifactsOnPass(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "upstream.json")
	if err := os.WriteFile(in, tbl(t, baseModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "dist")
	rep, err := Run(in, "", out, time.Now(), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("expected OK, got %s", violationsJoined(rep))
	}
	for _, name := range []string{"litellm.json", "index.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("expected %s written: %v", name, err)
		}
	}
}

func TestRun_WritesNothingOnFailure(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "upstream.json")
	if err := os.WriteFile(in, tbl(t, map[string]float64{"gpt-4o": 0.0000025}), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "dist")
	rep, err := Run(in, "", out, time.Now(), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.OK() {
		t.Fatal("expected validation to fail")
	}
	if _, err := os.Stat(filepath.Join(out, "litellm.json")); !os.IsNotExist(err) {
		t.Error("nothing should be written when validation fails")
	}
}

func TestRun_ReadsPreviousTable(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "upstream.json")
	prev := filepath.Join(dir, "prev.json")
	if err := os.WriteFile(in, tbl(t, baseModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prev, tbl(t, baseModels()), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(in, prev, filepath.Join(dir, "dist"), time.Now(), testCfg())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.OK() || rep.PreviousModels != 7 {
		t.Errorf("expected OK with prev=7, got ok=%v prev=%d", rep.OK(), rep.PreviousModels)
	}
}

func TestRun_MissingInputErrors(t *testing.T) {
	if _, err := Run(filepath.Join(t.TempDir(), "nope.json"), "", t.TempDir(), time.Now(), testCfg()); err == nil {
		t.Fatal("expected an error for a missing input file")
	}
}

func TestDefaultConfigIsSane(t *testing.T) {
	c := DefaultConfig()
	if c.MinModels <= 0 || c.MaxDropFraction <= 0 || c.MaxSwingFactor <= 1 || c.MaxSwingModels <= 0 || len(c.RequiredModels) == 0 {
		t.Errorf("DefaultConfig looks unsafe: %+v", c)
	}
}
