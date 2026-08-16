package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const desktopWallpaperMax = 8 << 20

type desktopPrefs struct {
	Mode  string `json:"mode"`  // center | stretch | cover
	Color string `json:"color"` // desktop face behind a centered/cover picture
	Rev   int64  `json:"rev"`
	Image bool   `json:"image"`
}

func (s *Server) desktopDir() string { return filepath.Join(s.StateDir, "desktop") }
func (s *Server) desktopPrefsPath() string {
	return filepath.Join(s.desktopDir(), "prefs.json")
}
func (s *Server) desktopWallpaperPath() string {
	return filepath.Join(s.desktopDir(), "wallpaper")
}

func normalizeDesktopMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "stretch":
		return "stretch"
	case "cover", "fill", "expand", "expand-all":
		return "cover"
	default:
		return "center"
	}
}

func normalizeDesktopColor(c string) string {
	c = strings.TrimSpace(c)
	if len(c) == 4 && c[0] == '#' { // #abc
		return strings.ToLower(c)
	}
	if len(c) == 7 && c[0] == '#' {
		return strings.ToLower(c)
	}
	return "#ccccff"
}

func (s *Server) loadDesktopPrefs() desktopPrefs {
	p := desktopPrefs{Mode: "center", Color: "#ccccff"}
	b, err := os.ReadFile(s.desktopPrefsPath())
	if err == nil {
		_ = json.Unmarshal(b, &p)
	}
	p.Mode = normalizeDesktopMode(p.Mode)
	p.Color = normalizeDesktopColor(p.Color)
	if _, err := os.Stat(s.desktopWallpaperPath()); err == nil {
		p.Image = true
	} else {
		p.Image = false
	}
	return p
}

func (s *Server) saveDesktopPrefs(p desktopPrefs) error {
	if err := os.MkdirAll(s.desktopDir(), 0o755); err != nil {
		return err
	}
	p.Mode = normalizeDesktopMode(p.Mode)
	p.Color = normalizeDesktopColor(p.Color)
	if p.Rev == 0 {
		p.Rev = time.Now().UnixMilli()
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.desktopPrefsPath(), append(b, '\n'), 0o644)
}

func (s *Server) handleDesktopGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadDesktopPrefs())
}

func (s *Server) handleDesktopPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode  string `json:"mode"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p := s.loadDesktopPrefs()
	if req.Mode != "" {
		p.Mode = req.Mode
	}
	if req.Color != "" {
		p.Color = req.Color
	}
	p.Rev = time.Now().UnixMilli()
	if err := s.saveDesktopPrefs(p); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.loadDesktopPrefs())
}

func sniffImage(b []byte) string {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg"
	}
	if len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(b) >= 2 && string(b[:2]) == "BM" {
		return "image/bmp"
	}
	return ""
}

func (s *Server) handleDesktopWallpaperGet(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(s.desktopWallpaperPath())
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("no wallpaper"))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ct := sniffImage(b)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Write(b)
}

func (s *Server) handleDesktopWallpaperPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, desktopWallpaperMax+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(body) > desktopWallpaperMax {
		writeErr(w, http.StatusRequestEntityTooLarge, errors.New("picture larger than 8 MB"))
		return
	}
	if sniffImage(body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("not a JPEG, PNG, GIF, WebP or BMP"))
		return
	}
	if err := os.MkdirAll(s.desktopDir(), 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.WriteFile(s.desktopWallpaperPath(), body, 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	p := s.loadDesktopPrefs()
	p.Rev = time.Now().UnixMilli()
	_ = s.saveDesktopPrefs(p)
	writeJSON(w, http.StatusOK, s.loadDesktopPrefs())
}

func (s *Server) handleDesktopWallpaperDelete(w http.ResponseWriter, r *http.Request) {
	_ = os.Remove(s.desktopWallpaperPath())
	p := s.loadDesktopPrefs()
	p.Rev = time.Now().UnixMilli()
	_ = s.saveDesktopPrefs(p)
	writeJSON(w, http.StatusOK, s.loadDesktopPrefs())
}
