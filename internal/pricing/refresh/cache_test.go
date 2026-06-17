package refresh

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCache_WriteReadFresh(t *testing.T) {
	path := CachePath(t.TempDir())
	if _, ok := ReadFreshCache(path, time.Hour); ok {
		t.Error("missing cache should not be fresh")
	}
	if err := WriteCache(path, []byte(`{"m":1}`)); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}
	b, ok := ReadFreshCache(path, time.Hour)
	if !ok || string(b) != `{"m":1}` {
		t.Errorf("read back = %q ok=%v", b, ok)
	}
}

func TestCache_Stale(t *testing.T) {
	path := CachePath(t.TempDir())
	if err := WriteCache(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Backdate the file beyond the freshness window.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadFreshCache(path, 24*time.Hour); ok {
		t.Error("a 48h-old cache should be stale under a 24h window")
	}
}

func TestWriteCache_BadPath(t *testing.T) {
	// A regular file stands where a directory is needed, so MkdirAll must fail.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteCache(filepath.Join(file, "sub", "litellm.json"), []byte("y")); err == nil {
		t.Error("WriteCache beneath a file path should error")
	}
}

func TestReadFreshCache_Unreadable(t *testing.T) {
	// A directory at the cache path: Stat succeeds and looks fresh, but ReadFile fails.
	p := filepath.Join(t.TempDir(), "cachedir")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadFreshCache(p, time.Hour); ok {
		t.Error("a directory should not read as a fresh cache file")
	}
}

func TestCachePath(t *testing.T) {
	if got := CachePath("/home/.aispend"); got != filepath.Join("/home/.aispend", "pricing", "litellm.json") {
		t.Errorf("CachePath = %q", got)
	}
}
