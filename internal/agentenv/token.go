package agentenv

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DeliveryToken is a one-shot authorized download handle.
type DeliveryToken struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Label   string    `json:"label"`
	Used    bool      `json:"used"`
	Expires time.Time `json:"expires"`
}

type tokenStore struct {
	mu    sync.Mutex
	path  string
	Items map[string]*DeliveryToken `json:"items"`
}

func openTokens(stateDir string) (*tokenStore, error) {
	p := filepath.Join(stateDir, "dl-tokens.json")
	st := &tokenStore{path: p, Items: map[string]*DeliveryToken{}}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &st.Items); err != nil {
		// tolerate older {"items":...} wrapper
		var wrap struct {
			Items map[string]*DeliveryToken `json:"items"`
		}
		if json.Unmarshal(b, &wrap) == nil && wrap.Items != nil {
			st.Items = wrap.Items
		}
	}
	if st.Items == nil {
		st.Items = map[string]*DeliveryToken{}
	}
	return st, nil
}

func (st *tokenStore) save() error {
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st.Items, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// IssueToken records a one-shot download for path. ttl<=0 means 24h.
func IssueToken(stateDir, filePath, label string, ttl time.Duration) (*DeliveryToken, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	t := &DeliveryToken{
		ID:      hex.EncodeToString(buf),
		Path:    filePath,
		Label:   label,
		Expires: time.Now().Add(ttl),
	}
	st, err := openTokens(stateDir)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Items[t.ID] = t
	if err := st.save(); err != nil {
		return nil, err
	}
	return t, nil
}

var (
	ErrTokenMissing = errors.New("unknown download token")
	ErrTokenUsed    = errors.New("download token already used")
	ErrTokenExpired = errors.New("download token expired")
)

// RedeemToken returns the file path and marks the token used.
func RedeemToken(stateDir, id string) (*DeliveryToken, error) {
	st, err := openTokens(stateDir)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	t, ok := st.Items[id]
	if !ok {
		return nil, ErrTokenMissing
	}
	if t.Used {
		return nil, ErrTokenUsed
	}
	if time.Now().After(t.Expires) {
		return nil, ErrTokenExpired
	}
	t.Used = true
	if err := st.save(); err != nil {
		return nil, err
	}
	return t, nil
}

// LookupToken is a non-consuming peek (for tests and CLI print).
func LookupToken(stateDir, id string) (*DeliveryToken, error) {
	st, err := openTokens(stateDir)
	if err != nil {
		return nil, err
	}
	t, ok := st.Items[id]
	if !ok {
		return nil, ErrTokenMissing
	}
	cp := *t
	return &cp, nil
}
