//go:build !offline

package quotaonline

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/quota"
)

// NetworkEnabled is true in the default build: online quota can reach the usage APIs.
// `aispend doctor --network` reads this to disclose the potential outbound.
const NetworkEnabled = true

// FetchClaude reads Anthropic's OAuth usage endpoint with cred and maps it to samples.
// observedAt stamps freshness. Best-effort by contract: any transport, status, or parse
// error returns (nil, err) so the caller falls back to local sources.
func FetchClaude(ctx context.Context, cred Credential, observedAt time.Time) ([]quota.Sample, error) {
	b, err := getJSON(ctx, ClaudeUsageURL, map[string]string{
		"Authorization":  "Bearer " + cred.Token,
		"anthropic-beta": "oauth-2025-04-20",
		"Accept":         "application/json",
	})
	if err != nil {
		return nil, err
	}
	return quota.ParseClaudeUsageAPI(b, observedAt), nil
}

// FetchCodex reads the ChatGPT backend usage endpoint with cred and maps it to samples.
func FetchCodex(ctx context.Context, cred Credential, observedAt time.Time) ([]quota.Sample, error) {
	h := map[string]string{
		"Authorization": "Bearer " + cred.Token,
		"Accept":        "application/json",
	}
	if cred.AccountID != "" {
		h["ChatGPT-Account-Id"] = cred.AccountID
	}
	b, err := getJSON(ctx, CodexUsageURL, h)
	if err != nil {
		return nil, err
	}
	return quota.ParseCodexUsageAPI(b, observedAt), nil
}

// getJSON GETs url with the given headers. It host-pins redirects (the request can never
// bounce the bearer token to a third-party host), caps the body, and bounds the wait.
// Only the supplied headers are sent — the caller's own token to the vendor's official
// endpoint — no cookies, no telemetry. Errors never include header values.
func getJSON(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	c := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing cross-host redirect to %q", req.URL.Host)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("quotaonline: request %s: %w", url, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req) //nolint:gosec // intended: the user's own OAuth token to the vendor's official usage endpoint
	if err != nil {
		return nil, fmt.Errorf("quotaonline: get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quotaonline: get %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
}
