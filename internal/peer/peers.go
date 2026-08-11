package peer

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Peer is one enrolled node. Identity is the public key; Addr is only the
// last known address (Tailscale IPs can move between reinstalls).
type Peer struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	PubKey  string    `json:"pubkey"` // base64 raw ed25519
	Addr    string    `json:"addr"`   // host:port of the peer's API listener
	AddedAt time.Time `json:"added_at"`
}

// Key decodes the peer's public key.
func (p *Peer) Key() (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(p.PubKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("bad peer public key")
	}
	return ed25519.PublicKey(b), nil
}

// Store persists the peer list in its own file — deliberately NOT in
// config.json, whose UI does whole-object GET-then-PUT and would clobber a
// concurrently added peer.
type Store struct {
	path  string
	mu    sync.Mutex
	peers map[string]*Peer
}

func LoadStore(stateDir string) (*Store, error) {
	s := &Store{path: filepath.Join(stateDir, "peers.json"), peers: map[string]*Peer{}}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	} else if err != nil {
		return nil, err
	}
	var f struct {
		Peers []*Peer `json:"peers"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for _, p := range f.Peers {
		if p != nil && p.ID != "" {
			s.peers[p.ID] = p
		}
	}
	return s, nil
}

// saveLocked writes atomically (temp + rename); callers hold s.mu.
func (s *Store) saveLocked() error {
	list := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(map[string]any{"peers": list}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".peers-*")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), s.path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

func (s *Store) List() []*Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*Peer, 0, len(s.peers))
	for _, p := range s.peers {
		cp := *p
		list = append(list, &cp)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name+list[i].ID < list[j].Name+list[j].ID })
	return list
}

func (s *Store) Get(id string) (*Peer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok {
		return nil, false
	}
	cp := *p
	return &cp, true
}

// Upsert adds or refreshes a peer. The public key is trust-on-enroll: an
// existing peer's key never changes silently — a mismatch is an error.
func (s *Store) Upsert(p *Peer) error {
	if p.ID == "" || p.PubKey == "" {
		return errors.New("peer missing id or key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.peers[p.ID]; ok {
		if old.PubKey != p.PubKey {
			return errors.New("peer key mismatch for " + p.ID)
		}
		if p.Name != "" {
			old.Name = p.Name
		}
		if p.Addr != "" {
			old.Addr = p.Addr
		}
		return s.saveLocked()
	}
	if p.AddedAt.IsZero() {
		p.AddedAt = time.Now().UTC()
	}
	cp := *p
	s.peers[p.ID] = &cp
	return s.saveLocked()
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[id]; !ok {
		return errors.New("no such peer")
	}
	delete(s.peers, id)
	return s.saveLocked()
}
