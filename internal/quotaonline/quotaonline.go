// Package quotaonline is the OPT-IN, net-gated bridge to the providers' authenticated
// usage APIs — the only way to read Claude's live plan-limit windows, which Claude Code
// does not persist to disk (see design-documents/DESIGN.md). It mirrors package
// pricing/refresh: net/* lives only in the //go:build !offline files (fetch_online.go),
// while the //go:build offline stub (fetch_offline.go) imports no network code, so an
// air-gapped binary stays provably net-free. The pure parsing lives in package quota.
//
// It reads the SAME local OAuth credential the vendor CLI already stored (never asking
// the user for a token) and sends it only to that vendor's official endpoint. The token
// is never logged and never persisted by aispend. It is OFF by default; `doctor
// --network` discloses the potential outbound when enabled.
package quotaonline

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Base endpoints. They are vars, not consts, so tests can point them at an httptest
// server; production never rewrites them.
var (
	// ClaudeUsageURL is Anthropic's OAuth usage endpoint (the 5h / weekly / Opus / Sonnet
	// windows), the same one Claude Code's own client uses.
	ClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	// CodexUsageURL is the ChatGPT backend usage endpoint (primary / secondary windows).
	CodexUsageURL = "https://chatgpt.com/backend-api/wham/usage"
)

// Credential is a provider's local OAuth bearer token plus, for Codex, the account id
// sent as a header. Loaded from the same file/Keychain the vendor CLI wrote; held only
// for the duration of one fetch, never logged, never persisted by aispend.
type Credential struct {
	Token     string
	AccountID string // Codex only: the ChatGPT-Account-Id header
}

type claudeCredFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// ParseClaudeCredential extracts the bearer token from Claude Code's credential blob
// (`~/.claude/.credentials.json` or the macOS Keychain item), keyed claudeAiOauth.
func ParseClaudeCredential(raw []byte) (Credential, error) {
	var f claudeCredFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Credential{}, fmt.Errorf("quotaonline: parse claude credential: %w", err)
	}
	if f.ClaudeAiOauth.AccessToken == "" {
		return Credential{}, errors.New("quotaonline: no claudeAiOauth.accessToken in credential")
	}
	return Credential{Token: f.ClaudeAiOauth.AccessToken}, nil
}

type codexAuthFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// ParseCodexAuth extracts the bearer token and account id from Codex's auth blob
// (`~/.codex/auth.json` / `~/.config/codex/auth.json` or the Keychain item).
func ParseCodexAuth(raw []byte) (Credential, error) {
	var f codexAuthFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Credential{}, fmt.Errorf("quotaonline: parse codex auth: %w", err)
	}
	if f.Tokens.AccessToken == "" {
		return Credential{}, errors.New("quotaonline: no tokens.access_token in codex auth")
	}
	return Credential{Token: f.Tokens.AccessToken, AccountID: f.Tokens.AccountID}, nil
}
