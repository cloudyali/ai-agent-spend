package quotaonline

import "testing"

func TestParseClaudeCredential(t *testing.T) {
	raw := []byte(`{"claudeAiOauth":{"accessToken":"sk-abc","subscriptionType":"max","scopes":["user:profile"]}}`)
	c, err := ParseClaudeCredential(raw)
	if err != nil || c.Token != "sk-abc" {
		t.Fatalf("token=%q err=%v", c.Token, err)
	}
}

func TestParseClaudeCredential_Missing(t *testing.T) {
	if _, err := ParseClaudeCredential([]byte(`{"claudeAiOauth":{}}`)); err == nil {
		t.Error("missing accessToken should error")
	}
	if _, err := ParseClaudeCredential([]byte(`not json`)); err == nil {
		t.Error("garbage should error")
	}
}

func TestParseCodexAuth(t *testing.T) {
	raw := []byte(`{"tokens":{"access_token":"jwt.tok","account_id":"acct-9"},"OPENAI_API_KEY":"x"}`)
	c, err := ParseCodexAuth(raw)
	if err != nil || c.Token != "jwt.tok" || c.AccountID != "acct-9" {
		t.Fatalf("cred=%+v err=%v", c, err)
	}
}

func TestParseCodexAuth_Missing(t *testing.T) {
	if _, err := ParseCodexAuth([]byte(`{"tokens":{}}`)); err == nil {
		t.Error("missing access_token should error")
	}
}
