// Package codex signs in to a ChatGPT subscription with the same OAuth flow
// the Codex CLI uses, and speaks the OpenAI Responses API on
// chatgpt.com/backend-api/codex — so the Chat window can run on a ChatGPT
// plan instead of an API key.
package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// clientID is OpenAI's OAuth client registration for the Codex CLI; the
	// registration pins the redirect URI to exactly this localhost URL.
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	authorizeURL = "https://auth.openai.com/oauth/authorize"
	tokenURL     = "https://auth.openai.com/oauth/token"
	scope        = "openid profile email offline_access"
	originator   = "exe"

	// CallbackAddr listens on all interfaces: the client registration pins
	// the redirect to localhost, so when the browser runs on another machine
	// the user swaps localhost for the daemon's host in the address bar and
	// the same code+state land here. The random state gates the exchange.
	CallbackAddr = ":1455"
	CallbackPath = "/auth/callback"
	RedirectURI  = "http://localhost:1455/auth/callback"
)

// Creds is the stored outcome of a sign-in: the bearer token pair plus the
// identity claims decoded from the access token's JWT payload.
type Creds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	// Expires is when AccessToken stops being accepted, in unix milliseconds.
	Expires   int64  `json:"expires"`
	AccountID string `json:"account_id"`
	Plan      string `json:"plan,omitempty"`
	Email     string `json:"email,omitempty"`
}

// Flow is one pending browser sign-in: the PKCE verifier and state stay on
// this side, URL is what the user's browser opens.
type Flow struct {
	State    string
	Verifier string
	URL      string
}

func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func NewFlow() (*Flow, error) {
	verifier, err := randB64(64)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	sb := make([]byte, 16)
	if _, err := rand.Read(sb); err != nil {
		return nil, err
	}
	state := hex.EncodeToString(sb)
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {RedirectURI},
		"scope":                 {scope},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		// The trimmed-down consent flow the Codex CLI uses, with org claims
		// included in the id token.
		"codex_cli_simplified_flow":  {"true"},
		"id_token_add_organizations": {"true"},
		"originator":                 {originator},
	}
	return &Flow{State: state, Verifier: verifier, URL: authorizeURL + "?" + q.Encode()}, nil
}

// ParseRedirectInput extracts the authorization code from whatever the user
// pasted: the full redirect URL, a bare query string, or the code itself.
// When the input carries a state it must match the flow's.
func ParseRedirectInput(input, state string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", errors.New("empty input")
	}
	q := url.Values{}
	if u, err := url.Parse(input); err == nil && u.Query().Get("code") != "" {
		q = u.Query()
	} else if v, err := url.ParseQuery(strings.TrimPrefix(input, "?")); err == nil && v.Get("code") != "" {
		q = v
	}
	code := q.Get("code")
	if code == "" {
		// A bare code has no '&', '?' or spaces.
		if strings.ContainsAny(input, "&? \t\n") {
			return "", errors.New("no authorization code found in the pasted text")
		}
		return input, nil
	}
	if got := q.Get("state"); got != "" && got != state {
		return "", errors.New("state mismatch — start the sign-in again and paste the new URL")
	}
	return code, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai token endpoint: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if tr.AccessToken == "" || tr.ExpiresIn <= 0 {
		return nil, fmt.Errorf("token response missing fields: %s", truncate(string(raw), 500))
	}
	return &tr, nil
}

func credsFromToken(tr *tokenResponse, oldRefresh string) (*Creds, error) {
	c := &Creds{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Expires:      time.Now().UnixMilli() + tr.ExpiresIn*1000,
	}
	if c.RefreshToken == "" {
		c.RefreshToken = oldRefresh
	}
	if c.RefreshToken == "" {
		return nil, errors.New("token response carried no refresh token")
	}
	c.AccountID, c.Plan, c.Email = decodeClaims(tr.AccessToken)
	if c.AccountID == "" {
		return nil, errors.New("access token carried no chatgpt_account_id claim")
	}
	return c, nil
}

// Exchange trades an authorization code for tokens (PKCE completes here).
func Exchange(ctx context.Context, code, verifier string) (*Creds, error) {
	tr, err := postToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {RedirectURI},
	})
	if err != nil {
		return nil, err
	}
	return credsFromToken(tr, "")
}

// Refresh obtains a fresh access token; OpenAI rotates the refresh token,
// so the returned Creds must be persisted.
func Refresh(ctx context.Context, refreshToken string) (*Creds, error) {
	tr, err := postToken(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return nil, err
	}
	return credsFromToken(tr, refreshToken)
}

// decodeClaims pulls identity out of the access token's JWT payload — no
// network call, and no signature check needed: the token only talks to
// OpenAI, which verifies it itself.
func decodeClaims(accessToken string) (accountID, plan, email string) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", "", ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
			Plan      string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", "", ""
	}
	return claims.Auth.AccountID, claims.Auth.Plan, claims.Profile.Email
}

// ---- creds file ----

// LoadCreds reads the stored sign-in; (nil, nil) when there is none.
func LoadCreds(path string) (*Creds, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Creds
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.RefreshToken == "" {
		return nil, nil
	}
	return &c, nil
}

func SaveCreds(path string, c *Creds) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func DeleteCreds(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
