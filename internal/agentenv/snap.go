package agentenv

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Snap is a named copy of an Environment disk.
type Snap struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	Bytes     int64     `json:"bytes"`
}

func snapRoot(stateDir, vm string) string {
	return filepath.Join(stateDir, "vms", vm, "snaps")
}

func DiskPath(stateDir, vm string) string {
	return filepath.Join(stateDir, "vms", vm, "disk.raw")
}

// CreateSnap copies disk.raw into snaps/<id>/. Caller must stop the VM first.
func CreateSnap(stateDir, vm, label string) (Snap, error) {
	src := DiskPath(stateDir, vm)
	st, err := os.Stat(src)
	if err != nil {
		return Snap{}, fmt.Errorf("snapshot: disk %s: %w", src, err)
	}
	buf := make([]byte, 4)
	rand.Read(buf)
	id := fmt.Sprintf("%d-%s", time.Now().UTC().Unix(), hex.EncodeToString(buf))
	if label == "" {
		label = id
	}
	dir := filepath.Join(snapRoot(stateDir, vm), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Snap{}, err
	}
	dst := filepath.Join(dir, "disk.raw")
	if err := copyFile(src, dst); err != nil {
		os.RemoveAll(dir)
		return Snap{}, err
	}
	s := Snap{ID: id, Label: label, CreatedAt: time.Now().UTC(), Bytes: st.Size()}
	meta, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "snap.json"), meta, 0o644); err != nil {
		os.RemoveAll(dir)
		return Snap{}, err
	}
	return s, nil
}

func ListSnaps(stateDir, vm string) ([]Snap, error) {
	ents, err := os.ReadDir(snapRoot(stateDir, vm))
	if err != nil {
		if os.IsNotExist(err) {
			return []Snap{}, nil
		}
		return nil, err
	}
	var out []Snap
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(snapRoot(stateDir, vm), e.Name(), "snap.json"))
		if err != nil {
			continue
		}
		var s Snap
		if json.Unmarshal(b, &s) == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// SnapDisk is the on-disk file for a snapshot, or an error if it is missing.
func SnapDisk(stateDir, vm, id string) (string, error) {
	src := filepath.Join(snapRoot(stateDir, vm), id, "disk.raw")
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("snapshot %s: %w", id, err)
	}
	return src, nil
}

// RestoreSnap copies a snap's disk over the live disk. Caller must stop the VM.
func RestoreSnap(stateDir, vm, id string) error {
	src := filepath.Join(snapRoot(stateDir, vm), id, "disk.raw")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("snapshot %s: %w", id, err)
	}
	return copyFile(src, DiskPath(stateDir, vm))
}

func DeleteSnap(stateDir, vm, id string) error {
	p := filepath.Join(snapRoot(stateDir, vm), id)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("snapshot %s: %w", id, err)
	}
	return os.RemoveAll(p)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}
