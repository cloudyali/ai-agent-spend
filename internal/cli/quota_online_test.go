package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/quota"
	"github.com/cloudyali/ai-agent-spend/internal/quotaonline"
)

func TestQuotaSamples_PreferOnlineWhenAvailable(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	a := appWithHome(t.TempDir(), &strings.Builder{}, now)
	a.fetchQuota = func(provider string, _ time.Time) []quota.Sample {
		if provider != "claude" {
			return nil
		}
		return []quota.Sample{{Provider: "claude", Window: quota.WindowWeekly, UsedPercent: 81, ResetsAt: now.Add(24 * time.Hour), ObservedAt: now}}
	}
	got := a.claudeQuotaSamples(now)
	if len(got) != 1 || got[0].UsedPercent != 81 {
		t.Fatalf("online samples should win over the local snapshot, got %+v", got)
	}
}

func TestQuotaSamples_FallBackToLocalWhenOnlineEmpty(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	writeUsage(t, home, `{"rate_limits":{"seven_day":{"used_percentage":78,"resets_at":1750507200}}}`)
	a := appWithHome(home, &strings.Builder{}, now)
	a.fetchQuota = func(string, time.Time) []quota.Sample { return nil } // online yields nothing
	got := a.claudeQuotaSamples(now)
	if len(got) != 1 || got[0].UsedPercent != 78 {
		t.Fatalf("should fall back to the local snapshot, got %+v", got)
	}
}

// The real credential path: a token file on disk + the usage API (httptest) → samples.
func TestLiveQuotaSamples_ClaudeFromFileAndAPI(t *testing.T) {
	now := time.Now()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"tok"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"seven_day":{"utilization":42,"resets_at":1782000000}}`))
	}))
	defer srv.Close()
	old := quotaonline.ClaudeUsageURL
	quotaonline.ClaudeUsageURL = srv.URL
	defer func() { quotaonline.ClaudeUsageURL = old }()

	a := appWithHome(home, &strings.Builder{}, now)
	got := a.liveQuotaSamples("claude", now)
	if len(got) != 1 || got[0].UsedPercent != 42 {
		t.Fatalf("want the live 42%% sample from file credential + API, got %+v", got)
	}
}

func TestLiveQuotaSamples_NoCredential(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	if got := a.liveQuotaSamples("claude", time.Now()); got != nil {
		t.Errorf("no claude credential → nil, got %+v", got)
	}
	if got := a.liveQuotaSamples("codex", time.Now()); got != nil {
		t.Errorf("no codex credential → nil, got %+v", got)
	}
	if got := a.liveQuotaSamples("nope", time.Now()); got != nil {
		t.Errorf("unknown provider → nil, got %+v", got)
	}
}
