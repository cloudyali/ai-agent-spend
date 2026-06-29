// Package claudecode implements the Provider interface for Claude Code, reading
// session JSONL from the OS-aware roots resolved by internal/platform.
//
// It detects the agent, enumerates sources (so unsupported records can be
// reported, never silently dropped), and reads records line by line. Per-file
// incremental state (offset/mtime) is tracked by the store's scan_state in the
// scan pipeline; Read here applies a coarse mtime filter against `since`.
//
// See design-documents/DESIGN.md Code provider.
package claudecode

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/provider"
)

// Compile-time proof we satisfy the Provider seam.
var _ provider.Provider = (*Provider)(nil)

// Provider reads Claude Code sessions located via the platform resolver.
type Provider struct {
	res platform.Resolver
}

// New returns a Claude Code provider bound to a platform resolver.
func New(res platform.Resolver) *Provider { return &Provider{res: res} }

// Name identifies this provider in events and evidence.
func (p *Provider) Name() string { return "claude_code" }

// Detect reports whether Claude Code data exists on this machine.
func (p *Provider) Detect() (bool, error) {
	return len(p.res.ExistingRoots("claude_code")) > 0, nil
}

// Sources enumerates every *.jsonl session file under the existing roots.
func (p *Provider) Sources() ([]provider.Source, error) {
	var srcs []provider.Source
	for _, root := range p.res.ExistingRoots("claude_code") {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// The Cowork session tree nests transcripts beside large
				// generated/upload trees; never descend those — a .jsonl there is
				// an artifact, not a Claude Code transcript, and walking them is slow.
				switch d.Name() {
				case "outputs", "uploads", "node_modules":
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".jsonl") {
				srcs = append(srcs, provider.Source{
					PathHash: platform.HashPath(path, p.res.GOOS),
					RawPath:  path,
					Kind:     "session_jsonl",
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
// `since` (a zero `since` reads everything). The raw path rides along in memory
// only — it is never persisted.
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
		fileRecs, err := provider.ReadJSONL("claude_code", s)
		if err != nil {
			return nil, err
		}
		recs = append(recs, fileRecs...)
	}
	return recs, nil
}
