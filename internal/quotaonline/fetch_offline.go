//go:build offline

package quotaonline

import (
	"context"
	"errors"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/quota"
)

// NetworkEnabled is false in the air-gapped build: no net/* is compiled into this
// package, so `go list -deps -tags offline ./...` stays provably network-free and online
// quota is unavailable (the menu bar falls back to local sources).
const NetworkEnabled = false

var errOffline = errors.New("quotaonline: offline build — online quota disabled")

// FetchClaude is disabled in the offline build; the signature matches the online build
// so callers compile unchanged (no net/* imported here).
func FetchClaude(context.Context, Credential, time.Time) ([]quota.Sample, error) {
	return nil, errOffline
}

// FetchCodex is disabled in the offline build.
func FetchCodex(context.Context, Credential, time.Time) ([]quota.Sample, error) {
	return nil, errOffline
}
