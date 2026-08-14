package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// The proxy is the only thing standing between the guest and the daemon's
// GitHub token while a push is in flight, so its pinning must hold: one
// repository path, auth injected upstream only, redirects rewritten back
// through the proxy.
func TestGitHubProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livid/app.git/redir" {
			w.Header().Set("Location", "http://"+r.Host+"/livid/app.git/info/refs")
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		io.WriteString(w, "auth="+r.Header.Get("Authorization"))
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	local := httptest.NewServer(githubProxy(u, "tok123", "/livid/app.git", "http://127.0.0.1:9999"))
	defer local.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	get := func(path string) *http.Response {
		t.Helper()
		resp, err := client.Get(local.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Any other repository — including a prefix cousin — is forbidden.
	for _, p := range []string{"/livid/other.git/info/refs", "/livid/app.gitx/info/refs", "/", "/livid"} {
		resp := get(p)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s: got %d, want 403", p, resp.StatusCode)
		}
	}

	// The pinned repository passes through with the token as Basic auth.
	resp := get("/livid/app.git/info/refs?service=git-receive-pack")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pinned path: got %d, want 200", resp.StatusCode)
	}
	want := "auth=Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:tok123"))
	if string(body) != want {
		t.Errorf("upstream saw %q, want %q", body, want)
	}

	// Upstream redirects come back pointing at the proxy, not upstream.
	resp = get("/livid/app.git/redir")
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "http://127.0.0.1:9999/livid/app.git/info/refs" {
		t.Errorf("Location = %q, want the localBase rewrite", loc)
	}
}
