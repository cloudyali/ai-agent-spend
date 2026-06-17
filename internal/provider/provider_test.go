package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadJSONL_RecordsLinesAndProvenance(t *testing.T) {
	dir := t.TempDir()
	// Blank lines (including whitespace-only) are skipped, but line numbers count
	// every physical line so a record's Line maps back to the source file.
	body := "{\"a\":1}\n" + "\n" + "   \n" + "{\"b\":2}\n"
	path := writeFile(t, dir, "s.jsonl", body)
	src := Source{PathHash: "deadbeef", RawPath: path, Kind: "session_jsonl"}

	recs, err := ReadJSONL("claude_code", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (blank lines skipped)", len(recs))
	}
	if recs[0].Line != 1 || recs[1].Line != 4 {
		t.Errorf("line numbers = %d,%d; want 1,4", recs[0].Line, recs[1].Line)
	}
	if recs[0].Provider != "claude_code" || recs[0].Source.PathHash != "deadbeef" {
		t.Errorf("provenance wrong: %+v", recs[0])
	}
	if string(recs[0].Raw) != `{"a":1}` {
		t.Errorf("raw = %q, want the trimmed line", recs[0].Raw)
	}
}

func TestReadJSONL_FinalLineWithoutNewline(t *testing.T) {
	dir := t.TempDir()
	// A file whose last line has no trailing newline must still yield that record.
	path := writeFile(t, dir, "s.jsonl", "{\"a\":1}\n{\"b\":2}")
	recs, err := ReadJSONL("codex", Source{RawPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (EOF without newline keeps the last record)", len(recs))
	}
	if string(recs[1].Raw) != `{"b":2}` {
		t.Errorf("last raw = %q, want {\"b\":2}", recs[1].Raw)
	}
}

func TestReadJSONL_HandlesVeryLongLines(t *testing.T) {
	dir := t.TempDir()
	// Real agent sessions embed large tool outputs on a single line, past
	// bufio.Scanner's default cap — bufio.Reader must read it whole.
	big := strings.Repeat("x", 2<<20) // 2 MiB
	path := writeFile(t, dir, "big.jsonl", `{"blob":"`+big+"\"}\n")
	recs, err := ReadJSONL("claude_code", Source{RawPath: path})
	if err != nil {
		t.Fatalf("ReadJSONL on a long line: %v", err)
	}
	if len(recs) != 1 || len(recs[0].Raw) < 2<<20 {
		t.Fatalf("got %d records (raw len %d), want 1 full long line", len(recs), len(recs[0].Raw))
	}
}

func TestReadJSONL_EmptyFileYieldsNoRecords(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "empty.jsonl", "")
	recs, err := ReadJSONL("x", Source{RawPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("records = %d, want 0 for an empty file", len(recs))
	}
}

func TestReadJSONL_MissingFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")
	if _, err := ReadJSONL("x", Source{RawPath: missing}); err == nil {
		t.Error("expected an error opening a missing file")
	}
}
