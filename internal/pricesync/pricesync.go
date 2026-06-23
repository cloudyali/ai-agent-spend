// Package pricesync curates and validates the upstream LiteLLM price table before
// it is published to the aispend-hosted mirror (aispendllm.cloudyali.io). It is build/CI
// tooling, NOT part of the aispend binary: it imports no net/* (the workflow performs
// the single download) and reuses pricing.ParseLiteLLM so the gate matches exactly the
// map the client will price against. The published file mirrors the upstream LiteLLM
// JSON schema. See design-documents/05-llm-pricing.md.
package pricesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// IndexSchemaVersion is the version stamp on index.json.
const IndexSchemaVersion = 1

// Config tunes the validation gate.
type Config struct {
	MinModels       int      // floor on priced (canonicalized) models
	MaxDropFraction float64  // fail if priced count drops more than this vs the previous publish
	MaxSwingFactor  float64  // fail if a shared model's input price changes by >= this factor (or to 0)
	RequiredModels  []string // canonical anchor ids that must be present and priced
}

// DefaultConfig is the gate used by the daily pipeline. Anchors are deliberately
// rock-stable LiteLLM ids that act as canaries for a healthy table.
func DefaultConfig() Config {
	return Config{
		MinModels:       50,
		MaxDropFraction: 0.20,
		MaxSwingFactor:  10,
		RequiredModels:  []string{"gpt-4o", "gpt-3.5-turbo"},
	}
}

// Report is the outcome of a validation run. A non-empty Violations slice means the
// table must NOT be published (OK reports it); Added/Removed/Repriced are counts.
type Report struct {
	CurrentModels  int
	PreviousModels int
	Added          int
	Removed        int
	Repriced       int
	Violations     []string
}

// OK reports whether the table passed every gate.
func (r Report) OK() bool { return len(r.Violations) == 0 }

// entry is the price subset we compare on, matching the upstream LiteLLM schema.
type entry struct {
	InputCostPerToken float64 `json:"input_cost_per_token"`
}

func parseRaw(b []byte) (map[string]entry, error) {
	var m map[string]entry
	err := json.Unmarshal(b, &m)
	return m, err
}

// Validate gates curr (a freshly fetched upstream LiteLLM table) against prev (the
// last published table; may be nil/empty on first run, or unparseable, in which case
// the prev-relative checks are skipped). It returns an error only when curr itself is
// unusable; all policy failures land in Report.Violations so the caller can log them.
func Validate(curr, prev []byte, cfg Config) (Report, error) {
	var rep Report

	currEng, err := pricing.ParseLiteLLM(curr)
	if err != nil {
		return Report{}, fmt.Errorf("current table rejected by engine parser: %w", err)
	}
	currRaw, err := parseRaw(curr)
	if err != nil {
		return Report{}, fmt.Errorf("current table: %w", err)
	}
	rep.CurrentModels = len(currEng)

	var prevRaw map[string]entry
	if len(prev) > 0 {
		if prevEng, perr := pricing.ParseLiteLLM(prev); perr == nil {
			rep.PreviousModels = len(prevEng)
		}
		if pr, perr := parseRaw(prev); perr == nil {
			prevRaw = pr
		}
	}

	// Floor: never publish a table thinner than the floor.
	if rep.CurrentModels < cfg.MinModels {
		rep.Violations = append(rep.Violations,
			fmt.Sprintf("only %d priced models, below floor %d", rep.CurrentModels, cfg.MinModels))
	}

	// Cliff guard: a sudden mass disappearance is the "invalid cost map" failure mode.
	if rep.PreviousModels > 0 && cfg.MaxDropFraction > 0 {
		minAllowed := float64(rep.PreviousModels) * (1 - cfg.MaxDropFraction)
		if float64(rep.CurrentModels) < minAllowed {
			rep.Violations = append(rep.Violations,
				fmt.Sprintf("model count dropped %d→%d (more than %.0f%% drop)",
					rep.PreviousModels, rep.CurrentModels, cfg.MaxDropFraction*100))
		}
	}

	// Anchors: canary models the client prices on must survive (checked against the
	// canonicalized engine map, so id-format churn doesn't false-alarm).
	for _, m := range cfg.RequiredModels {
		if _, ok := currEng[m]; !ok {
			rep.Violations = append(rep.Violations,
				fmt.Sprintf("required anchor model %q missing or unpriced", m))
		}
	}

	// Per-model swing guard, on raw upstream ids shared by both tables.
	if prevRaw != nil {
		for id, ce := range currRaw {
			pe, ok := prevRaw[id]
			if !ok {
				rep.Added++
				continue
			}
			if pe.InputCostPerToken <= 0 || ce.InputCostPerToken == pe.InputCostPerToken {
				continue
			}
			rep.Repriced++
			if ce.InputCostPerToken <= 0 {
				rep.Violations = append(rep.Violations,
					fmt.Sprintf("%s input price collapsed to 0 (was %g)", id, pe.InputCostPerToken))
				continue
			}
			ratio := ce.InputCostPerToken / pe.InputCostPerToken
			if cfg.MaxSwingFactor > 0 && (ratio >= cfg.MaxSwingFactor || ratio <= 1/cfg.MaxSwingFactor) {
				rep.Violations = append(rep.Violations,
					fmt.Sprintf("%s input price swung %g→%g (%.1fx)", id, pe.InputCostPerToken, ce.InputCostPerToken, ratio))
			}
		}
		for id := range prevRaw {
			if _, ok := currRaw[id]; !ok {
				rep.Removed++
			}
		}
	}

	sort.Strings(rep.Violations)
	return rep, nil
}

// Artifacts is the set of files the pipeline publishes.
type Artifacts struct {
	LiteLLM []byte // litellm.json — schema-preserving, deterministically serialized
	Index   []byte // index.json — provenance + checksum
	SHA256  string // hex sha256 of LiteLLM
}

type index struct {
	SchemaVersion int               `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	ModelCount    int               `json:"model_count"`
	Files         map[string]string `json:"files"` // filename → sha256
}

// Build produces the publishable artifacts from a validated table. encoding/json sorts
// map keys, so the serialized table is deterministic (a clean daily git diff) and
// lossless (every upstream field preserved via json.RawMessage).
func Build(curr []byte, now time.Time) (Artifacts, error) {
	eng, err := pricing.ParseLiteLLM(curr)
	if err != nil {
		return Artifacts{}, fmt.Errorf("refusing to build from an unparseable table: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(curr, &raw); err != nil {
		return Artifacts{}, fmt.Errorf("build: %w", err)
	}
	llm, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Artifacts{}, fmt.Errorf("build: %w", err)
	}
	llm = append(llm, '\n')

	sum := sha256.Sum256(llm)
	shaHex := hex.EncodeToString(sum[:])
	idx, err := json.MarshalIndent(index{
		SchemaVersion: IndexSchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		ModelCount:    len(eng),
		Files:         map[string]string{"litellm.json": shaHex},
	}, "", "  ")
	if err != nil {
		return Artifacts{}, fmt.Errorf("build index: %w", err)
	}
	idx = append(idx, '\n')

	return Artifacts{LiteLLM: llm, Index: idx, SHA256: shaHex}, nil
}

// Run is the file-IO orchestration the CLI wraps: read upstream (+ optional previous
// published table), validate, and on a clean pass write litellm.json + index.json into
// outDir. On any violation it writes nothing and returns the Report so the caller can
// fail the job and keep serving the last-good table.
func Run(inPath, prevPath, outDir string, now time.Time, cfg Config) (Report, error) {
	curr, err := os.ReadFile(inPath)
	if err != nil {
		return Report{}, err
	}
	var prev []byte
	if prevPath != "" {
		if b, e := os.ReadFile(prevPath); e == nil {
			prev = b
		}
	}

	rep, err := Validate(curr, prev, cfg)
	if err != nil {
		return rep, err
	}
	if !rep.OK() {
		return rep, nil // caller decides the exit; nothing is written
	}

	art, err := Build(curr, now)
	if err != nil {
		return rep, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return rep, err
	}
	for name, data := range map[string][]byte{"litellm.json": art.LiteLLM, "index.json": art.Index} {
		if err := os.WriteFile(filepath.Join(outDir, name), data, 0o644); err != nil {
			return rep, err
		}
	}
	return rep, nil
}
