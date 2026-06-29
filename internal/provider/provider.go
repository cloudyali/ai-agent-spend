// Package provider defines the Provider interface — one implementation per AI
// coding agent — and the RawRecord/Source types that carry un-normalized data
// from an agent's local files into the normalizer.
//
// Phase 0A ships exactly one provider: claudecode. Adding a fleet source later
// (e.g. a Cursor Admin API poller) is just another implementation feeding the
// same AgentEvent schema — the seam, not a rewrite.
//
// See design-documents/DESIGN.md
package provider

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"time"
)

// Source identifies one place an agent stores records. PathHash is the only form
// ever persisted or exported; RawPath is in-memory only, used while reading.
type Source struct {
	PathHash string
	RawPath  string
	Kind     string // e.g. "session_jsonl"
}

// RawRecord is one un-parsed unit (e.g. a single JSONL line) plus its provenance.
type RawRecord struct {
	Provider string
	Source   Source
	Line     int
	Raw      []byte
}

// Provider detects an installed agent and reads its raw records. One per agent.
type Provider interface {
	Name() string                              // "claude_code"
	Detect() (present bool, err error)         // is this agent on the machine?
	Sources() ([]Source, error)                // enables unsupported-source reporting, not silent drops
	Read(since time.Time) ([]RawRecord, error) // only records newer than the last scan
}

// ReadJSONL reads one file's non-empty lines as RawRecords, tagged with the given
// provider name. It uses bufio.Reader (not Scanner) so arbitrarily long lines —
// real agent sessions embed large tool outputs on a single line — are handled.
// Shared by every JSONL-based provider so the large-line fix lives in one place.
func ReadJSONL(name string, s Source) ([]RawRecord, error) {
	f, err := os.Open(s.RawPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var recs []RawRecord
	r := bufio.NewReader(f)
	for i := 1; ; i++ {
		line, err := r.ReadBytes('\n')
		if raw := bytes.TrimSpace(line); len(raw) > 0 {
			recs = append(recs, RawRecord{
				Provider: name,
				Source:   s,
				Line:     i,
				Raw:      append([]byte(nil), raw...),
			})
		}
		if err != nil {
			if err == io.EOF {
				return recs, nil
			}
			return nil, err
		}
	}
}
