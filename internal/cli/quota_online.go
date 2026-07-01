package cli

import (
	"context"
	"os"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/quota"
	"github.com/cloudyali/ai-agent-spend/internal/quotaonline"
)

// onlineQuotaTimeout bounds each best-effort usage-API fetch so a slow endpoint never
// stalls a glance; on timeout the caller falls back to local sources.
const onlineQuotaTimeout = 3 * time.Second

// onlineSamples returns a provider's live plan-limit samples: the injected test hook when
// set (so unit tests stay hermetic), else the real credential-loading fetch. The reading
// passes through the last-good cache (rememberQuota) so a single empty fetch can't blank a
// gauge the menu bar just showed.
func (a *App) onlineSamples(provider string, now time.Time) []quota.Sample {
	var fresh []quota.Sample
	if a.fetchQuota != nil {
		fresh = a.fetchQuota(provider, now)
	} else {
		fresh = a.liveQuotaSamples(provider, now)
	}
	return a.rememberQuota(provider, fresh)
}

// rememberQuota smooths a transient empty fetch: a non-empty reading becomes the provider's
// last-good and is returned as-is; an empty reading (a per-refresh network hiccup) falls
// back to the last-good, so one dropped fetch can't blank a live gauge — which would also
// reorder the provider under an idle peer. Staleness isn't re-checked here: quota.Tracker
// .Active already drops any window past its reset at render, so a cached window that expires
// mid-outage falls off there (one source of truth). The cache is bounded — one entry per
// provider, overwritten on success. Locked because the menu bar can refresh concurrently
// (the interval ticker plus a manual Refresh).
func (a *App) rememberQuota(provider string, fresh []quota.Sample) []quota.Sample {
	a.quotaCacheMu.Lock()
	defer a.quotaCacheMu.Unlock()
	if len(fresh) > 0 {
		if a.quotaCache == nil {
			a.quotaCache = map[string][]quota.Sample{}
		}
		a.quotaCache[provider] = fresh
	}
	return a.quotaCache[provider]
}

// liveQuotaSamples loads the provider's local OAuth credential and reads its live windows
// from the usage API — the same source OpenUsage uses, and the only way to see Claude's
// windows (Claude Code keeps them behind the API, not on disk). Best-effort: the offline
// build, a missing credential, or any transport/parse error yields nil, so the caller
// falls back to local sources. Bounded by onlineQuotaTimeout.
func (a *App) liveQuotaSamples(provider string, now time.Time) []quota.Sample {
	if !quotaonline.NetworkEnabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), onlineQuotaTimeout)
	defer cancel()
	switch provider {
	case "claude":
		cred, ok := a.claudeCredential()
		if !ok {
			return nil
		}
		if s, err := quotaonline.FetchClaude(ctx, cred, now); err == nil {
			return s
		}
	case "codex":
		cred, ok := a.codexCredential()
		if !ok {
			return nil
		}
		if s, err := quotaonline.FetchCodex(ctx, cred, now); err == nil {
			return s
		}
	}
	return nil
}

// claudeCredential loads Claude's OAuth credential: the on-disk file first, then the
// macOS Keychain (darwin only). ok is false when none is available or parseable.
func (a *App) claudeCredential() (quotaonline.Credential, bool) {
	if b, err := os.ReadFile(a.Resolver.ClaudeCredentialsPath()); err == nil {
		if c, err := quotaonline.ParseClaudeCredential(b); err == nil {
			return c, true
		}
	}
	if b, ok := keychainSecret("Claude Code-credentials"); ok {
		if c, err := quotaonline.ParseClaudeCredential(b); err == nil {
			return c, true
		}
	}
	return quotaonline.Credential{}, false
}

// codexCredential loads Codex's OAuth credential from its candidate auth.json paths, then
// the macOS Keychain (darwin only).
func (a *App) codexCredential() (quotaonline.Credential, bool) {
	for _, p := range a.Resolver.CodexAuthPaths() {
		if b, err := os.ReadFile(p); err == nil {
			if c, err := quotaonline.ParseCodexAuth(b); err == nil {
				return c, true
			}
		}
	}
	if b, ok := keychainSecret("Codex Auth"); ok {
		if c, err := quotaonline.ParseCodexAuth(b); err == nil {
			return c, true
		}
	}
	return quotaonline.Credential{}, false
}
