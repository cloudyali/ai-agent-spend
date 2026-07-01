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

// quotaRefreshInterval throttles the usage-API fetch. The menu bar repaints every ~30s, but
// plan windows move slowly (5-hour / weekly), so hitting the API that often is wasteful and
// risks rate limits. We fetch at most this often per provider and serve the last-good
// reading in between (see rememberQuota); the countdown + ledger still refresh every repaint.
// 5–10m is plenty fresh for a weekly wall — tune here.
const quotaRefreshInterval = 5 * time.Minute

// onlineSamples returns a provider's live plan-limit samples. It is throttled: within
// quotaRefreshInterval of the last fetch it serves the cached reading and skips the network
// entirely (the menu bar repaints far faster than plan windows move); otherwise it fetches
// — the injected test hook when set (so unit tests stay hermetic), else the real
// credential-loading fetch — and remembers the result. rememberQuota keeps the last-good, so
// an empty or throttled fetch can't blank a gauge the menu bar just showed.
func (a *App) onlineSamples(provider string, now time.Time) []quota.Sample {
	if cached, fresh := a.cachedQuota(provider, now); !fresh {
		return cached
	}
	var got []quota.Sample
	if a.fetchQuota != nil {
		got = a.fetchQuota(provider, now)
	} else {
		got = a.liveQuotaSamples(provider, now)
	}
	return a.rememberQuota(provider, got, now)
}

// cachedQuota reports whether a fresh fetch is due for a provider. When the last fetch was
// within quotaRefreshInterval it returns (last-good, false) — serve this, don't hit the
// network; otherwise (nil, true) — a fetch is due. Locked for the menu bar's concurrent
// ticker+manual refreshes.
func (a *App) cachedQuota(provider string, now time.Time) (samples []quota.Sample, fetchDue bool) {
	a.quotaCacheMu.Lock()
	defer a.quotaCacheMu.Unlock()
	if at, ok := a.quotaFetchedAt[provider]; ok && now.Sub(at) < quotaRefreshInterval {
		return a.quotaCache[provider], false
	}
	return nil, true
}

// rememberQuota records the fetch time (throttling the next one, whether this fetch
// succeeded or came back empty) and caches a non-empty reading as the provider's last-good.
// On an empty reading it returns the prior last-good, so a dropped fetch can't blank a live
// gauge — which would also reorder the provider under an idle peer. Staleness isn't
// re-checked here: quota.Tracker.Active already drops any window past its reset at render, so
// a cached window that expires mid-outage falls off there (one source of truth). The cache
// is bounded — one entry per provider. Locked for the menu bar's concurrent refreshes.
func (a *App) rememberQuota(provider string, fresh []quota.Sample, now time.Time) []quota.Sample {
	a.quotaCacheMu.Lock()
	defer a.quotaCacheMu.Unlock()
	if a.quotaFetchedAt == nil {
		a.quotaFetchedAt = map[string]time.Time{}
	}
	a.quotaFetchedAt[provider] = now
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
