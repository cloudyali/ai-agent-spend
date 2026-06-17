//go:build !offline

package refresh

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// NetworkEnabled is true in the default build: pricing can refresh from the
// endpoint. `aispend doctor --network` reads this to disclose the one outbound.
const NetworkEnabled = true

// fetchBytes performs the inbound GET. It attaches no cookies, no body, and no
// identifying headers — only a plain request for a public price file — and caps
// the response so a misbehaving endpoint can't exhaust memory.
func fetchBytes(url string) ([]byte, error) {
	c := &http.Client{
		Timeout: 10 * time.Second,
		// Pin redirects to the origin host: the price fetch talks only to the host it
		// started on, so a redirect can never bounce it to a third party.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("refusing cross-host redirect to %q", req.URL.Host)
			}
			return nil
		},
	}
	resp, err := c.Get(url) //nolint:gosec // intended: GET a public, non-identifying price file
	if err != nil {
		return nil, fmt.Errorf("refresh: get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh: get %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
}
