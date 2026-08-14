// Package github signs in to GitHub with the OAuth device flow and manages
// repositories over the REST API. The daemon holds the resulting token in
// ~/.exe/github.json; VMs publish through the daemon and never see it.
package github

import (
	"context"
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
	deviceCodeURL = "https://github.com/login/device/code"
	tokenURL      = "https://github.com/login/oauth/access_token"
	apiBase       = "https://api.github.com"

	// Scope covers private repositories too — creating and pushing a private
	// repo needs full "repo"; "public_repo" would forbid the default.
	scope = "repo"
)

// Creds is the stored outcome of a sign-in. OAuth-app tokens don't expire;
// a GitHub-App client returns a refresh pair instead, so both shapes fit.
type Creds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// Expires is when AccessToken stops being accepted, in unix
	// milliseconds; 0 means it does not expire.
	Expires int64  `json:"expires,omitempty"`
	Login   string `json:"login"`
	UserID  int64  `json:"user_id"`
}

// NoreplyEmail is the address GitHub associates with the account without
// exposing a real one — used as the commit author inside VMs.
func (c *Creds) NoreplyEmail() string {
	return fmt.Sprintf("%d+%s@users.noreply.github.com", c.UserID, c.Login)
}

// DeviceFlow is one pending device-code sign-in: the user enters UserCode
// at VerificationURI while the daemon polls with DeviceCode.
type DeviceFlow struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

func postForm(ctx context.Context, u string, form url.Values, out any) error {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github %s: HTTP %d: %s", u, resp.StatusCode, truncate(string(raw), 300))
	}
	return json.Unmarshal(raw, out)
}

// StartDeviceFlow asks GitHub for a fresh user code.
func StartDeviceFlow(ctx context.Context, clientID string) (*DeviceFlow, error) {
	var f DeviceFlow
	if err := postForm(ctx, deviceCodeURL, url.Values{"client_id": {clientID}, "scope": {scope}}, &f); err != nil {
		return nil, err
	}
	if f.DeviceCode == "" || f.UserCode == "" || f.VerificationURI == "" {
		return nil, errors.New("github device flow: response missing fields — is the client ID an OAuth app with device flow enabled?")
	}
	if f.Interval <= 0 {
		f.Interval = 5
	}
	return &f, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

var (
	errPending  = errors.New("authorization_pending")
	errSlowDown = errors.New("slow_down")
)

func pollOnce(ctx context.Context, clientID, deviceCode string) (*tokenResponse, error) {
	var tr tokenResponse
	err := postForm(ctx, tokenURL, url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}, &tr)
	if err != nil {
		return nil, err
	}
	switch tr.Error {
	case "":
	case "authorization_pending":
		return nil, errPending
	case "slow_down":
		return nil, errSlowDown
	case "expired_token":
		return nil, errors.New("the device code expired — start the sign-in again")
	case "access_denied":
		return nil, errors.New("sign-in was cancelled on GitHub")
	default:
		if tr.ErrorDesc != "" {
			return nil, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
		}
		return nil, errors.New(tr.Error)
	}
	if tr.AccessToken == "" {
		return nil, errors.New("github token response carried no access token")
	}
	return &tr, nil
}

func credsFromToken(tr *tokenResponse) *Creds {
	c := &Creds{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}
	if tr.ExpiresIn > 0 {
		c.Expires = time.Now().UnixMilli() + tr.ExpiresIn*1000
	}
	return c
}

// Wait polls until the user approves the device code (or ctx ends — the
// caller deadlines it at the flow's expiry). The returned Creds carry no
// identity yet; follow with FetchUser.
func Wait(ctx context.Context, clientID string, f *DeviceFlow) (*Creds, error) {
	interval := time.Duration(f.Interval) * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		tr, err := pollOnce(ctx, clientID, f.DeviceCode)
		switch {
		case errors.Is(err, errPending):
			continue
		case errors.Is(err, errSlowDown):
			interval += 5 * time.Second
			continue
		case err != nil:
			return nil, err
		}
		return credsFromToken(tr), nil
	}
}

// Refresh trades a refresh token for a fresh pair (GitHub-App clients only;
// OAuth-app tokens never expire and never take this path).
func Refresh(ctx context.Context, clientID, refreshToken string) (*Creds, error) {
	var tr tokenResponse
	err := postForm(ctx, tokenURL, url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, &tr)
	if err != nil {
		return nil, err
	}
	if tr.Error != "" || tr.AccessToken == "" {
		return nil, fmt.Errorf("github token refresh: %s — sign in again", tr.Error)
	}
	return credsFromToken(&tr), nil
}

// ---- REST API ----

func apiDo(ctx context.Context, method, path, token string, body, out any) (int, error) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(rctx, method, apiBase+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		var ge struct {
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &ge)
		if ge.Message == "" {
			ge.Message = truncate(string(raw), 200)
		}
		return resp.StatusCode, fmt.Errorf("github api %s: HTTP %d: %s", path, resp.StatusCode, ge.Message)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("parse github api %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// FetchUser fills the token's identity into c.
func FetchUser(ctx context.Context, c *Creds) error {
	var u struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if _, err := apiDo(ctx, http.MethodGet, "/user", c.AccessToken, nil, &u); err != nil {
		return err
	}
	if u.Login == "" {
		return errors.New("github /user returned no login")
	}
	c.Login, c.UserID = u.Login, u.ID
	return nil
}

type Repo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
	// Created is false when the repository already existed and was reused.
	Created bool `json:"-"`
}

// EnsureRepo creates login/name, or returns the existing repository when
// the name is already taken on the account.
func EnsureRepo(ctx context.Context, c *Creds, name string, private bool, description string) (*Repo, error) {
	var r Repo
	code, err := apiDo(ctx, http.MethodPost, "/user/repos", c.AccessToken, map[string]any{
		"name": name, "private": private, "description": description,
	}, &r)
	if err == nil {
		r.Created = true
		return &r, nil
	}
	if code != http.StatusUnprocessableEntity {
		return nil, err
	}
	// 422 usually means "name already exists on this account" — reuse it.
	if _, gerr := apiDo(ctx, http.MethodGet, "/repos/"+c.Login+"/"+name, c.AccessToken, nil, &r); gerr != nil {
		return nil, err // report the create failure, not the probe's
	}
	return &r, nil
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
	if c.AccessToken == "" {
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
