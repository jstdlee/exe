package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"exe/internal/codex"
	"exe/internal/hostagent"
)

// hostCodexCreds reads the host user's ~/.codex/auth.json — the same file
// `codex login` writes — so Chat does not need a second OAuth.
func hostCodexCreds() *codex.Creds {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return nil
	}
	var auth struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(b, &auth) != nil || auth.Tokens.AccessToken == "" {
		return nil
	}
	return &codex.Creds{
		AccessToken:  auth.Tokens.AccessToken,
		RefreshToken: auth.Tokens.RefreshToken,
		AccountID:    auth.Tokens.AccountID,
		Expires:      time.Now().Add(30 * time.Minute).UnixMilli(),
	}
}

func (s *Server) handleHostAgents(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"agents":     hostagent.List(),
		"host_agent": cfg.HostAgent,
		"host_model": cfg.HostModel,
	})
}
