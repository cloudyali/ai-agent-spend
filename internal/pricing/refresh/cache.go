package refresh

import (
	"os"
	"path/filepath"
	"time"
)

// LiteLLMURL is the aispend-hosted mirror of the community LiteLLM price table
// (github.com/BerriAI/litellm), served from aispendllm.cloudyali.io — a host we control,
// not a third party — so the laptop only ever talks to a host we can pin (see
// fetchBytes' same-host redirect guard). Fetching it is a single inbound GET for a
// public file: no spend, no identity, no telemetry. The endpoint serves the table
// directly (HTTP 200, no cross-host redirect) in the upstream LiteLLM JSON schema,
// published daily by the pricing-sync pipeline (.github/workflows/pricing-sync.yml).
const LiteLLMURL = "https://aispendllm.cloudyali.io/litellm.json"

// CachePath is the on-disk location of the last refreshed price table, under the
// app home so it travels with the rest of aispend's local state.
func CachePath(appHome string) string {
	return filepath.Join(appHome, "pricing", "litellm.json")
}

// ReadFreshCache returns the cached table bytes when the file exists and is no
// older than maxAge; ok is false when it is missing or stale. Pricing reads this
// offline — it never blocks on the network — so a stale cache simply isn't used.
func ReadFreshCache(path string, maxAge time.Duration) (data []byte, ok bool) {
	fi, err := os.Stat(path)
	if err != nil || time.Since(fi.ModTime()) > maxAge {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

// WriteCache stores refreshed table bytes, creating the directory as needed and
// swapping in atomically so a crash mid-write can't leave a half-written table.
func WriteCache(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
