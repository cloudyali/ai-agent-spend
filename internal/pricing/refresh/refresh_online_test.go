//go:build !offline

package refresh

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNetworkEnabled_DefaultBuild(t *testing.T) {
	if !NetworkEnabled {
		t.Error("default build must have NetworkEnabled = true")
	}
}

func TestFetch_GETsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"version":"test"}`))
	}))
	defer srv.Close()

	b, err := Fetch(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"version":"test"}` {
		t.Errorf("body = %s", b)
	}
}

func TestFetch_Non200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL); err == nil {
		t.Error("expected an error on a 404 response")
	}
}

func TestFetch_RefusesCrossHostRedirect(t *testing.T) {
	// A compromised or misconfigured upstream that 302s the price fetch to another
	// host must not be followed — the fetch is pinned to the host it started on.
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"exfiltrated":true}`))
	}))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer redirector.Close()

	if _, err := Fetch(redirector.URL); err == nil {
		t.Error("expected a cross-host redirect to be refused")
	}
}

func TestFetch_FollowsSameHostRedirect(t *testing.T) {
	// Same-host redirects (e.g. a CDN normalizing a path) are still followed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/table", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(`{"version":"test"}`))
	}))
	defer srv.Close()

	b, err := Fetch(srv.URL + "/redir")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"version":"test"}` {
		t.Errorf("body = %s, want the same-host redirect target", b)
	}
}
