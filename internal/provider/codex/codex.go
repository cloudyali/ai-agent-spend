// Package codex implements the Provider interface for the OpenAI Codex CLI,
// reading rollout JSONL from ~/.codex/sessions (date-based `YYYY/MM/DD/rollout-*`
// or thread-id `<uuid>/rollout.jsonl` layouts — a recursive glob handles both).
//
// Unlike Claude Code, a Codex turn's token usage lives in a separate TokenCount
// line from the TurnContext line that carries the model/cwd, so normalization is
// stateful per session — see internal/normalize (Codex). Format reference:
// openai/codex rollout docs and the ccusage/CodeBurn Codex parsers.
package codex

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/platform"
	"github.com/agentspend/ai-agent-spend/internal/provider"
)

var _ provider.Provider = (*Provider)(nil)

// Provider reads Codex rollout sessions located via the platform resolver.
type Provider struct {
	res platform.Resolver
}

// New returns a Codex provider bound to a platform resolver.
func New(res platform.Resolver) *Provider { return &Provider{res: res} }

// Name identifies this provider in events and evidence.
func (p *Provider) Name() string { return "codex" }

// Detect reports whether Codex session data exists on this machine.
func (p *Provider) Detect() (bool, error) {
	return len(p.res.ExistingRoots("codex")) > 0, nil
}

// Sources enumerates every *.jsonl rollout file under the existing roots.
func (p *Provider) Sources() ([]provider.Source, error) {
	var srcs []provider.Source
	for _, root := range p.res.ExistingRoots("codex") {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
				srcs = append(srcs, provider.Source{
					PathHash: platform.HashPath(path, p.res.GOOS),
					RawPath:  path,
					Kind:     "rollout_jsonl",
				})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return srcs, nil
}

// Read returns one RawRecord per non-empty line of each source modified after
// `since` (zero reads everything).
func (p *Provider) Read(since time.Time) ([]provider.RawRecord, error) {
	srcs, err := p.Sources()
	if err != nil {
		return nil, err
	}
	var recs []provider.RawRecord
	for _, s := range srcs {
		if !since.IsZero() {
			if fi, err := os.Stat(s.RawPath); err == nil && !fi.ModTime().After(since) {
				continue
			}
		}
		fileRecs, err := provider.ReadJSONL("codex", s)
		if err != nil {
			return nil, err
		}
		recs = append(recs, fileRecs...)
	}
	return recs, nil
}
