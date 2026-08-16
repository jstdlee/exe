package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSniffImage(t *testing.T) {
	if sniffImage([]byte{0xff, 0xd8, 0xff, 0xe0}) != "image/jpeg" {
		t.Fatal("jpeg")
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if sniffImage(png) != "image/png" {
		t.Fatal("png")
	}
	if sniffImage([]byte("GIF89a....")) != "image/gif" {
		t.Fatal("gif")
	}
	if sniffImage([]byte("not an image")) != "" {
		t.Fatal("reject")
	}
}

func TestDesktopPrefsAndWallpaper(t *testing.T) {
	dir := t.TempDir()
	s := &Server{StateDir: dir}
	p := s.loadDesktopPrefs()
	if p.Image || p.Mode != "center" {
		t.Fatalf("%+v", p)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	req := httptest.NewRequest(http.MethodPut, "/v1/desktop/wallpaper", bytes.NewReader(png))
	rr := httptest.NewRecorder()
	s.handleDesktopWallpaperPut(rr, req)
	if rr.Code != 200 {
		t.Fatalf("put %d %s", rr.Code, rr.Body.String())
	}
	p = s.loadDesktopPrefs()
	if !p.Image {
		t.Fatal("expected image")
	}
	if _, err := os.Stat(filepath.Join(dir, "desktop", "wallpaper")); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodPut, "/v1/desktop", bytes.NewReader([]byte(`{"mode":"expand-all","color":"#334455"}`)))
	rr = httptest.NewRecorder()
	s.handleDesktopPut(rr, req)
	if rr.Code != 200 {
		t.Fatalf("prefs %d %s", rr.Code, rr.Body.String())
	}
	var got desktopPrefs
	json.Unmarshal(rr.Body.Bytes(), &got)
	if got.Mode != "cover" || got.Color != "#334455" || !got.Image {
		t.Fatalf("%+v", got)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/desktop/wallpaper", nil)
	rr = httptest.NewRecorder()
	s.handleDesktopWallpaperDelete(rr, req)
	p = s.loadDesktopPrefs()
	if p.Image {
		t.Fatal("still has image")
	}
}
