package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// usageURL is the ChatGPT backend's account usage endpoint — the numbers
// behind the Codex CLI's /status display and the ChatGPT usage settings page.
const usageURL = "https://chatgpt.com/backend-api/wham/usage"

// UsageWindow is one rolling rate-limit window (the 5-hour or the weekly).
type UsageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

type UsageRateLimit struct {
	Allowed         bool         `json:"allowed"`
	LimitReached    bool         `json:"limit_reached"`
	PrimaryWindow   *UsageWindow `json:"primary_window"`
	SecondaryWindow *UsageWindow `json:"secondary_window"`
}

type UsageCredits struct {
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

// Usage is the subset of the usage response the Configuration tab shows.
type Usage struct {
	PlanType  string          `json:"plan_type"`
	RateLimit *UsageRateLimit `json:"rate_limit"`
	Credits   *UsageCredits   `json:"credits"`
}

// FetchUsage reads the subscription's current rate-limit usage and credit
// balance.
func FetchUsage(ctx context.Context, accessToken, accountID string) (*Usage, error) {
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, usageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("originator", originator)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chatgpt usage: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var u Usage
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, fmt.Errorf("parse chatgpt usage: %w", err)
	}
	return &u, nil
}
