//go:build offline

package refresh

import (
	"context"
	"errors"
)

// NetworkEnabled is false in the air-gapped build: no net/* is compiled in, so
// `go list -deps -tags offline ./...` is provably network-free.
const NetworkEnabled = false

// fetchBytes is disabled in the offline build; callers fall back to the embedded
// pricing tables (the always-present floor). The context parameter keeps the signature
// identical to the online build (no net/* imported here).
func fetchBytes(context.Context, string) ([]byte, error) {
	return nil, errors.New("refresh: offline build — pricing refresh disabled; using embedded tables")
}
