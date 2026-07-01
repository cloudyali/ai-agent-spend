//go:build !offline

package quotaonline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchClaude(t *testing.T) {
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":23,"resets_at":1782000000}}`))
	}))
	defer srv.Close()
	old := ClaudeUsageURL
	ClaudeUsageURL = srv.URL
	defer func() { ClaudeUsageURL = old }()

	got, err := FetchClaude(context.Background(), Credential{Token: "tok123"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].UsedPercent != 23 {
		t.Fatalf("want one 23%% sample, got %+v", got)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization header = %q, want Bearer tok123", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta header = %q", gotBeta)
	}
}

func TestFetchCodex_SendsAccountID(t *testing.T) {
	var gotAcct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcct = r.Header.Get("ChatGPT-Account-Id")
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":1,"reset_after_seconds":100,"limit_window_seconds":18000}}}`))
	}))
	defer srv.Close()
	old := CodexUsageURL
	CodexUsageURL = srv.URL
	defer func() { CodexUsageURL = old }()

	got, err := FetchCodex(context.Background(), Credential{Token: "t", AccountID: "acct-9"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PlanType != "pro" {
		t.Fatalf("want one pro sample, got %+v", got)
	}
	if gotAcct != "acct-9" {
		t.Errorf("ChatGPT-Account-Id header = %q", gotAcct)
	}
}

func TestFetchClaude_RefusesCrossHostRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
	}))
	defer srv.Close()
	old := ClaudeUsageURL
	ClaudeUsageURL = srv.URL
	defer func() { ClaudeUsageURL = old }()
	if _, err := FetchClaude(context.Background(), Credential{Token: "x"}, time.Now()); err == nil {
		t.Error("a cross-host redirect must be refused so the bearer token can't leak")
	}
}

func TestFetchClaude_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := ClaudeUsageURL
	ClaudeUsageURL = srv.URL
	defer func() { ClaudeUsageURL = old }()
	if _, err := FetchClaude(context.Background(), Credential{Token: "x"}, time.Now()); err == nil {
		t.Error("a non-200 should error so the caller falls back to local sources")
	}
}
