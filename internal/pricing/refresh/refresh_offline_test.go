//go:build offline

package refresh

import "testing"

func TestNetworkDisabled_OfflineBuild(t *testing.T) {
	if NetworkEnabled {
		t.Error("offline build must have NetworkEnabled = false")
	}
	if _, err := Fetch("https://agentspend.cloudyali.io/pricing/litellm.json"); err == nil {
		t.Error("offline Fetch must error so callers fall back to embedded tables")
	}
}
