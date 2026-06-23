// Package refresh pulls the live LiteLLM price table (see LiteLLMURL) into the
// local cache, offline-first. It is the ONLY package permitted to import net/* —
// and even that is confined to the default build: the //go:build offline variant
// (refresh_offline.go) imports no network code at all, so an air-gapped binary is
// provably net-free.
//
// Posture (locked): the shipped default ships embedded tables AND refresh ON
// (opt-out). Resolution precedence is cache → endpoint → embedded; pricing never
// blocks on the network. The one outbound is a single GET of the public,
// aispend-hosted price table — host-pinned and size-capped, sending no identity,
// its result only overlaying the embedded floor. See design-documents/05-llm-pricing.md §4.
package refresh

// Fetch returns the raw bytes of the price table at url: a plain inbound GET that
// sends no spend, no identity, no telemetry. In the offline build it always errors
// and callers fall back to the embedded tables.
func Fetch(url string) ([]byte, error) { return fetchBytes(url) }
