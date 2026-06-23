package normalize_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/normalize"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/provider"
)

var update = flag.Bool("update", false, "update golden files")

// fixedPriced removes clock nondeterminism from goldens (PricedAt is real-time
// in production, stamped by the pricing engine).
var fixedPriced = time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

// TestGolden_ClaudeCode pins the full normalize→price pipeline: every fixture
// session must produce byte-identical AgentEvent JSON. A Claude Code format
// change becomes a failing test here. Regenerate with: go test ./internal/normalize -update
func TestGolden_ClaudeCode(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "testdata", "providers", "claude_code", "v1", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found")
	}

	n := normalize.ClaudeCode{GOOS: "linux", IdentityHash: "id_demo"}
	eng := pricing.NewEngine()

	for _, fx := range fixtures {
		t.Run(filepath.Base(fx), func(t *testing.T) {
			events := normalizeAndPriceFile(t, fx, n, eng)
			got, err := json.MarshalIndent(events, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')

			golden := filepath.Join("..", "..", "testdata", "golden",
				strings.TrimSuffix(filepath.Base(fx), ".jsonl")+".json")

			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated %s", golden)
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update first): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s",
					filepath.Base(fx), got, want)
			}
		})
	}
}

func normalizeAndPriceFile(t *testing.T, path string, n normalize.ClaudeCode, eng *pricing.Engine) []event.AgentEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	pathHash := platform.HashPath("/fixtures/claude_code/"+filepath.Base(path), "linux")
	normalized := []event.AgentEvent{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for i := 1; sc.Scan(); i++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		rrec := provider.RawRecord{
			Provider: "claude_code",
			Source:   provider.Source{PathHash: pathHash, Kind: "session_jsonl"},
			Line:     i,
			Raw:      append([]byte(nil), raw...),
		}
		ev, err := n.Normalize(rrec)
		if errors.Is(err, normalize.ErrNotBillable) {
			continue
		}
		if err != nil {
			t.Fatalf("normalize line %d: %v", i, err)
		}
		normalized = append(normalized, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	// Mirror the scan pipeline: keep-max dedup before pricing, so the golden
	// reflects what `aispend scan` actually stores (streaming placeholders gone).
	normalized = n.Dedupe(normalized)

	out := []event.AgentEvent{}
	for i := range normalized {
		ev := normalized[i]
		if err := eng.Price(&ev, pricing.Plan{Kind: "api"}); err != nil {
			t.Fatalf("price %s: %v", ev.EventID, err)
		}
		ev.Evidence.PricedAt = fixedPriced
		out = append(out, ev)
	}
	return out
}
