package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"exe/internal/github"
)

// Sign in with GitHub: the OAuth device flow. The daemon shows a user code,
// the browser opens github.com/login/device, and a background goroutine
// polls until GitHub hands over the token — which then lives only on this
// host, in ~/.exe/github.json.

// ghRefreshMargin renews an expiring access token this long before its
// deadline (GitHub-App clients only; OAuth-app tokens never expire).
const ghRefreshMargin = 2 * time.Minute

// ghFlow is one pending device-code sign-in. err records a terminal poll
// failure so the UI's status polling can surface it.
type ghFlow struct {
	userCode        string
	verificationURI string
	expires         time.Time
	cancel          context.CancelFunc
	err             string
}

func (s *Server) ghCredsPath() string { return filepath.Join(s.StateDir, "github.json") }

// ghCreds returns the cached sign-in, loading the file on first use.
// Callers treat the result as read-only.
func (s *Server) ghCreds() *github.Creds {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	return s.ghCredsLocked()
}

func (s *Server) ghCredsLocked() *github.Creds {
	if !s.ghLoaded {
		s.ghLoaded = true
		c, err := github.LoadCreds(s.ghCredsPath())
		if err != nil {
			log.Printf("github: load credentials: %v", err)
		}
		s.ghCache = c
	}
	return s.ghCache
}

func (s *Server) ghStore(c *github.Creds) error {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	if err := github.SaveCreds(s.ghCredsPath(), c); err != nil {
		return err
	}
	s.ghCache, s.ghLoaded = c, true
	return nil
}

// ghToken returns live credentials, refreshing expiring ones (GitHub-App
// clients rotate the pair; the rotated pair is persisted).
func (s *Server) ghToken(ctx context.Context) (*github.Creds, error) {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	c := s.ghCredsLocked()
	if c == nil {
		return nil, errors.New("not signed in to GitHub (Configuration → GitHub)")
	}
	if c.Expires == 0 || time.Now().UnixMilli() < c.Expires-ghRefreshMargin.Milliseconds() {
		return c, nil
	}
	if c.RefreshToken == "" {
		return nil, errors.New("the GitHub token expired — sign in again (Configuration → GitHub)")
	}
	fresh, err := github.Refresh(ctx, s.Config().GitHub.ClientID, c.RefreshToken)
	if err != nil {
		return nil, err
	}
	fresh.Login, fresh.UserID = c.Login, c.UserID
	if err := github.SaveCreds(s.ghCredsPath(), fresh); err != nil {
		return nil, err
	}
	s.ghCache = fresh
	return fresh, nil
}

// handleGitHubStart begins a sign-in: it fetches a device code, hands the
// user code to the UI, and polls GitHub in the background until approval.
func (s *Server) handleGitHubStart(w http.ResponseWriter, r *http.Request) {
	clientID := s.Config().GitHub.ClientID
	if clientID == "" {
		writeErr(w, http.StatusConflict, errors.New("github.client_id is not configured (Configuration → GitHub)"))
		return
	}
	flow, err := github.StartDeviceFlow(r.Context(), clientID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	expires := time.Now().Add(time.Duration(flow.ExpiresIn) * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	f := &ghFlow{userCode: flow.UserCode, verificationURI: flow.VerificationURI, expires: expires, cancel: cancel}

	s.ghMu.Lock()
	if old := s.ghFlow; old != nil {
		old.cancel()
	}
	s.ghFlow = f
	s.ghMu.Unlock()

	go s.ghWait(ctx, f, clientID, flow)
	writeJSON(w, http.StatusOK, map[string]any{
		"user_code":        flow.UserCode,
		"verification_uri": flow.VerificationURI,
		"expires_in":       flow.ExpiresIn,
	})
}

// ghWait is the background poll for flow f; it consumes the flow on
// success and records the failure on it otherwise.
func (s *Server) ghWait(ctx context.Context, f *ghFlow, clientID string, flow *github.DeviceFlow) {
	defer f.cancel()
	creds, err := github.Wait(ctx, clientID, flow)
	if err == nil {
		err = github.FetchUser(ctx, creds)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = errors.New("the sign-in code expired — start again")
	}
	s.ghMu.Lock()
	superseded := s.ghFlow != f // a newer sign-in or a sign-out won
	if !superseded {
		if err != nil {
			f.err = err.Error()
		} else {
			s.ghFlow = nil
		}
	}
	s.ghMu.Unlock()
	if superseded || errors.Is(err, context.Canceled) {
		return
	}
	if err != nil {
		log.Printf("github: sign-in failed: %v", err)
		return
	}
	if err := s.ghStore(creds); err != nil {
		log.Printf("github: store credentials: %v", err)
		return
	}
	log.Printf("github: signed in as %s", creds.Login)
	s.PostNews("vm", "GitHub connected", "Signed in as "+creds.Login+" — VMs can now publish repositories.")
}

// handleGitHubStatus reports the sign-in state, the pending device code if
// a flow is underway, and any terminal error that flow hit.
func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	res := map[string]any{
		"authenticated": false,
		"configured":    s.Config().GitHub.ClientID != "",
	}
	s.ghMu.Lock()
	if f := s.ghFlow; f != nil {
		if f.err != "" {
			res["error"] = f.err
		} else if time.Now().Before(f.expires) {
			res["pending"] = true
			res["user_code"] = f.userCode
			res["verification_uri"] = f.verificationURI
		}
	}
	s.ghMu.Unlock()
	if c := s.ghCreds(); c != nil {
		res["authenticated"] = true
		res["login"] = c.Login
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGitHubLogout(w http.ResponseWriter, r *http.Request) {
	s.ghMu.Lock()
	if f := s.ghFlow; f != nil {
		f.cancel()
	}
	s.ghFlow = nil
	err := github.DeleteCreds(s.ghCredsPath())
	s.ghCache, s.ghLoaded = nil, true
	s.ghMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

// ghRequireAuth is the precheck shared by publish endpoints: a token must
// exist before any guest-side work starts.
func (s *Server) ghRequireAuth(ctx context.Context) (*github.Creds, error) {
	c, err := s.ghToken(ctx)
	if err != nil {
		return nil, err
	}
	if c.Login == "" {
		return nil, fmt.Errorf("stored GitHub sign-in carries no login — sign in again")
	}
	return c, nil
}
