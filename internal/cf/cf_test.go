package cf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// toServer sends every request to ts, whatever host it names.
type toServer struct{ ts *httptest.Server }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.ts.URL)
	r.URL.Scheme, r.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestContentTypeOnlyWithBody(t *testing.T) {
	got := map[string]string{}
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got[r.Method] = r.Header.Get("Content-Type")
		auth = r.Header.Get("Authorization")
		io.WriteString(w, `{"success":true,"errors":[],"result":{}}`)
	}))
	defer ts.Close()
	c := &Client{Token: " tok\n", HTTPC: &http.Client{Transport: toServer{ts}}}
	ctx := context.Background()
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.do(ctx, http.MethodPost, "/x", map[string]string{"a": "b"}, nil); err != nil {
		t.Fatal(err)
	}
	if got["GET"] != "" {
		t.Errorf("GET Content-Type = %q, want none", got["GET"])
	}
	if got["POST"] != "application/json" {
		t.Errorf("POST Content-Type = %q", got["POST"])
	}
	if auth != "Bearer tok" {
		t.Errorf("Authorization = %q", auth)
	}
}
