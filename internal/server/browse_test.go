package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowseStripsFrameGuardsAndRewrites(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><body><a href="/skills.md">x</a><img src="https://example.com/i.png"></body></html>`))
	}))
	defer upstream.Close()

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v1/browse?url="+upstream.URL+"/page", nil)
	rr := httptest.NewRecorder()
	s.handleBrowse(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("X-Frame-Options leaked: %q", got)
	}
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors") || strings.Contains(csp, "'none'") {
		t.Fatalf("csp = %q", csp)
	}
	if !strings.Contains(csp, "frame-src 'self'") {
		t.Fatalf("csp should keep iframe navigations on the browse proxy: %q", csp)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "/v1/browse/http/") {
		t.Fatalf("links not rewritten to path form: %s", body)
	}
	if strings.Contains(body, `href="/skills.md"`) {
		t.Fatalf("relative href survived: %s", body)
	}
	if !strings.Contains(body, "window.fetch") {
		t.Fatalf("helper script missing: %s", body)
	}
}

func TestBrowsePathForm(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(`body{background:url(/bg.png)}`))
	}))
	defer upstream.Close()
	req := httptest.NewRequest(http.MethodGet, "/v1/browse/http/"+strings.TrimPrefix(upstream.URL, "http://")+"/x.css", nil)
	rr := httptest.NewRecorder()
	(&Server{}).handleBrowse(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/v1/browse/http/") {
		t.Fatalf("css url not rewritten: %s", rr.Body.String())
	}
}

func TestBrowseHelperResolvesRootRelativeFetchesAgainstUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head></head><body></body></html>`))
	}))
	defer upstream.Close()

	req := httptest.NewRequest(http.MethodGet, "/v1/browse?url="+upstream.URL+"/page", nil)
	rr := httptest.NewRecorder()
	(&Server{}).handleBrowse(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `var remoteBase=`) {
		t.Fatalf("helper should carry upstream base URL: %s", body)
	}
	if strings.Contains(body, `new URL(u, document.baseURI)`) {
		t.Fatalf("helper must not resolve root-relative fetches against local proxy URL: %s", body)
	}
	if !strings.Contains(body, `new URL(u, remoteBase)`) {
		t.Fatalf("helper should resolve fetch/XHR URLs against upstream base: %s", body)
	}
	if !strings.Contains(body, `HTMLIFrameElement`) || !strings.Contains(body, `MutationObserver`) {
		t.Fatalf("helper should rewrite dynamically-created framed resources: %s", body)
	}
}

func TestBrowseRejectsBadURL(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/v1/browse?url=file:///etc/passwd", nil)
	rr := httptest.NewRecorder()
	s.handleBrowse(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rr.Code)
	}
}
