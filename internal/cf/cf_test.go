package cf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyTokenGETHasNoContentType(t *testing.T) {
	var gotCT, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/tokens/verify" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"errors":[],"messages":[],"result":{"id":"x","status":"active"}}`)
	}))
	defer ts.Close()
	c := &Client{Token: "  tok_abc  \n", Base: ts.URL}
	if err := c.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotCT != "" {
		t.Fatalf("Content-Type=%q on GET (Cloudflare 6003)", gotCT)
	}
	if gotAuth != "Bearer tok_abc" {
		t.Fatalf("Authorization=%q", gotAuth)
	}
}

func TestDoPOSTSetsJSONContentType(t *testing.T) {
	var gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		io.WriteString(w, `{"success":true,"result":{}}`)
	}))
	defer ts.Close()
	c := &Client{Token: "t", Base: ts.URL}
	if err := c.do(context.Background(), "POST", "/accounts", map[string]string{"x": "y"}, nil); err != nil {
		t.Fatal(err)
	}
	if gotCT != "application/json" {
		t.Fatalf("POST Content-Type=%q", gotCT)
	}
}
