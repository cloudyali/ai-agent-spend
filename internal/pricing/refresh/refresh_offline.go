//go:build offline

package refresh

import "errors"

// NetworkEnabled is false in the air-gapped build: no net/* is compiled in, so
// `go list -deps -tags offline ./...` is provably network-free.
const NetworkEnabled = false

// fetchBytes is disabled in the offline build; callers fall back to the embedded
// pricing tables (the always-present floor).
func fetchBytes(string) ([]byte, error) {
	return nil, errors.New("refresh: offline build — pricing refresh disabled; using embedded tables")
}
