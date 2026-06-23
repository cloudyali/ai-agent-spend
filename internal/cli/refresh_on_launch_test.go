package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
)

// a small but valid LiteLLM table the injected fetcher returns (no real network).
const fakeLiteLLMTable = `{"claude-opus-4":{"input_cost_per_token":0.00001,"output_cost_per_token":0.00001,"cache_read_input_token_cost":0.000001}}`

// refreshApp builds a hermetic App rooted at home with an injected price fetcher; the
// counter records how many times the (fake) network fetch was invoked.
func refreshApp(t *testing.T, home string, fetched *int) (*App, *strings.Builder) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".aispend"), 0o755); err != nil {
		t.Fatal(err)
	}
	var errb strings.Builder
	a := &App{
		Resolver: platform.Resolver{GOOS: "linux", Home: home, Env: func(string) string { return "" }},
		Now:      func() time.Time { return time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC) },
		Out:      &strings.Builder{},
		Err:      &errb,
		fetchPrices: func(context.Context, string) ([]byte, error) {
			*fetched++
			return []byte(fakeLiteLLMTable), nil
		},
	}
	return a, &errb
}

// A missing/stale cache is topped up on launch: one fetch, the cache is written, and a
// notice lands on stderr.
func TestRefreshOnLaunch_StaleFetchesAndCaches(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("offline build never fetches")
	}
	t.Setenv("AISPEND_NO_REFRESH", "")
	home := t.TempDir()
	fetched := 0
	a, errb := refreshApp(t, home, &fetched)

	a.refreshOnLaunch(false)

	if fetched != 1 {
		t.Fatalf("stale cache should fetch exactly once, got %d", fetched)
	}
	if _, err := os.Stat(refresh.CachePath(a.Resolver.AppHome())); err != nil {
		t.Errorf("refresh should have written the cache: %v", err)
	}
	if !strings.Contains(errb.String(), "refreshed") {
		t.Errorf("expected a refresh notice on stderr, got: %q", errb.String())
	}
}

// A cache written within the last 24h is already fresh: no fetch, no notice.
func TestRefreshOnLaunch_FreshCacheSkipsFetch(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("offline build never fetches")
	}
	t.Setenv("AISPEND_NO_REFRESH", "")
	home := t.TempDir()
	fetched := 0
	a, errb := refreshApp(t, home, &fetched)
	if err := refresh.WriteCache(refresh.CachePath(a.Resolver.AppHome()), []byte(fakeLiteLLMTable)); err != nil {
		t.Fatal(err)
	}

	a.refreshOnLaunch(false)

	if fetched != 0 {
		t.Errorf("fresh cache must not fetch, got %d", fetched)
	}
	if strings.Contains(errb.String(), "refreshed") {
		t.Errorf("fresh cache → no notice, got: %q", errb.String())
	}
}

// Every opt-out path (the --no-refresh flag, AISPEND_NO_REFRESH, refresh_on_launch=false)
// suppresses the fetch even when the cache is stale.
func TestRefreshOnLaunch_OptOuts(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("offline build never fetches")
	}
	t.Run("skip flag", func(t *testing.T) {
		t.Setenv("AISPEND_NO_REFRESH", "")
		home := t.TempDir()
		fetched := 0
		a, _ := refreshApp(t, home, &fetched)
		if n, did := a.refreshIfStale(context.Background(), true); did || n != 0 {
			t.Errorf("skip=true must not refresh: n=%d did=%v", n, did)
		}
		if fetched != 0 {
			t.Errorf("skip must not fetch, got %d", fetched)
		}
	})
	t.Run("env disables", func(t *testing.T) {
		t.Setenv("AISPEND_NO_REFRESH", "1")
		home := t.TempDir()
		fetched := 0
		a, _ := refreshApp(t, home, &fetched)
		if _, did := a.refreshIfStale(context.Background(), false); did {
			t.Error("AISPEND_NO_REFRESH=1 must disable refresh")
		}
		if fetched != 0 {
			t.Errorf("env off must not fetch, got %d", fetched)
		}
	})
	t.Run("config disables", func(t *testing.T) {
		t.Setenv("AISPEND_NO_REFRESH", "")
		home := t.TempDir()
		fetched := 0
		a, _ := refreshApp(t, home, &fetched)
		writeAppConfig(t, home, "refresh_on_launch = false\n")
		if _, did := a.refreshIfStale(context.Background(), false); did {
			t.Error("refresh_on_launch=false must disable refresh")
		}
		if fetched != 0 {
			t.Errorf("config off must not fetch, got %d", fetched)
		}
	})
}

// A failed or unparseable fetch is best-effort: no cache is written, refreshIfStale
// reports (0,false), and pricing falls back to the existing cache / embedded floor.
func TestRefreshIfStale_FetchFailureFallsBack(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("offline build never fetches")
	}
	t.Setenv("AISPEND_NO_REFRESH", "")
	t.Run("fetch error", func(t *testing.T) {
		home := t.TempDir()
		a, _ := refreshApp(t, home, new(int))
		a.fetchPrices = func(context.Context, string) ([]byte, error) { return nil, errors.New("network down") }
		if n, did := a.refreshIfStale(context.Background(), false); did || n != 0 {
			t.Errorf("fetch error → no refresh: n=%d did=%v", n, did)
		}
		if _, err := os.Stat(refresh.CachePath(a.Resolver.AppHome())); err == nil {
			t.Error("a failed fetch must not write a cache")
		}
	})
	t.Run("unparseable payload", func(t *testing.T) {
		home := t.TempDir()
		a, _ := refreshApp(t, home, new(int))
		a.fetchPrices = func(context.Context, string) ([]byte, error) { return []byte("not json"), nil }
		if _, did := a.refreshIfStale(context.Background(), false); did {
			t.Error("a bad payload must not count as a refresh")
		}
	})
}

// The fetch honors the caller's context, so the bounded one-shot launch top-up aborts
// (and proceeds on cached/embedded rates) rather than hanging when the deadline passes.
func TestRefreshIfStale_HonorsContext(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("offline build never fetches")
	}
	t.Setenv("AISPEND_NO_REFRESH", "")
	home := t.TempDir()
	a, _ := refreshApp(t, home, new(int))
	a.fetchPrices = func(ctx context.Context, _ string) ([]byte, error) { return nil, ctx.Err() }
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // deadline already passed
	if _, did := a.refreshIfStale(ctx, false); did {
		t.Error("a cancelled context must abort the refresh")
	}
}

// With no injected fetcher, priceFetcher falls back to the real (network) fetch — a
// non-nil function — so production fetches while tests inject their own.
func TestPriceFetcher_DefaultsToNetwork(t *testing.T) {
	a := &App{}
	if a.priceFetcher() == nil {
		t.Error("priceFetcher() must never return nil")
	}
}
