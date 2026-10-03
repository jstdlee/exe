// Package snap keeps point-in-time copies of stopped VM disks. A copy is a
// reflink when the filesystem can share blocks (FICLONE on Linux btrfs/XFS,
// clonefile on APFS), else a sparse copy that writes only the disk's data
// blocks; never a dense full copy. The caller stops the VM first.
package snap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// ErrNotFound means no snapshot has that id.
var ErrNotFound = errors.New("snapshot not found")

// Meta describes one snapshot (meta.json).
type Meta struct {
	ID        string    `json:"id"`
	VM        string    `json:"vm"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Size is the disk's apparent size; Used what the copy occupies on the
	// host (0 where the platform cannot tell). A reflink shares blocks with
	// the disk, so Used overstates what it costs.
	Size   int64  `json:"size"`
	Used   int64  `json:"used_bytes,omitempty"`
	Method string `json:"method"` // "reflink" or "sparse"
}

// DiskPath is where every vmm backend keeps a VM's disk:
// <state>/vms/<name>/disk.raw (Firecracker, Virtualization.framework, QEMU).
func DiskPath(stateDir, vm string) string {
	return filepath.Join(stateDir, "vms", vm, "disk.raw")
}

// Store keeps snapshots under Root/<vm>/<id>/{disk.raw,meta.json}.
type Store struct{ Root string }

const (
	diskFile = "disk.raw"
	metaFile = "meta.json"
)

var idRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// ValidID reports whether id has the shape New gives ids, so it is safe as
// a path element.
func ValidID(id string) bool { return idRE.MatchString(id) }

func newID(now time.Time) string {
	var b [3]byte
	rand.Read(b[:])
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func (s Store) dir(vm, id string) string { return filepath.Join(s.Root, vm, id) }

// List returns vm's snapshots, newest first.
func (s Store) List(vm string) ([]Meta, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, vm))
	if errors.Is(err, fs.ErrNotExist) {
		return []Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Meta{}
	for _, e := range entries {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		m, err := s.Get(vm, e.Name())
		if err != nil {
			continue // partial or damaged: skipped
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Get reads one snapshot's metadata.
func (s Store) Get(vm, id string) (Meta, error) {
	if !ValidID(id) {
		return Meta{}, ErrNotFound
	}
	data, err := os.ReadFile(filepath.Join(s.dir(vm, id), metaFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("snapshot %s: %w", id, err)
	}
	return m, nil
}

// Create copies disk into a new snapshot of vm. The VM must be stopped.
func (s Store) Create(ctx context.Context, vm, disk, label string) (Meta, error) {
	st, err := os.Stat(disk)
	if err != nil {
		return Meta{}, fmt.Errorf("disk of %s: %w", vm, err)
	}
	now := time.Now()
	id := newID(now)
	dir := s.dir(vm, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Meta{}, err
	}
	fail := func(err error) (Meta, error) {
		os.RemoveAll(dir)
		return Meta{}, err
	}
	dst := filepath.Join(dir, diskFile)
	method, err := Copy(ctx, disk, dst)
	if err != nil {
		return fail(err)
	}
	m := Meta{ID: id, VM: vm, Label: label, CreatedAt: now.UTC(), Size: st.Size(), Method: method}
	if dst, err := os.Stat(dst); err == nil {
		m.Used = allocated(dst)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := writeFileAtomic(filepath.Join(dir, metaFile), append(data, '\n')); err != nil {
		return fail(err)
	}
	return m, nil
}

// Restore replaces disk with the snapshot's copy. The VM must be stopped.
// The new disk keeps the old one's mode and owner; it lands by rename, so a
// failed copy leaves the old disk untouched.
func (s Store) Restore(ctx context.Context, vm, id, disk string) error {
	if _, err := s.Get(vm, id); err != nil {
		return err
	}
	old, err := os.Stat(disk)
	if err != nil {
		return fmt.Errorf("disk of %s: %w", vm, err)
	}
	tmp := disk + ".jx-restore"
	os.Remove(tmp)
	if _, err := Copy(ctx, filepath.Join(s.dir(vm, id), diskFile), tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, old.Mode().Perm()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := chownLike(tmp, old); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, disk); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Delete removes one snapshot.
func (s Store) Delete(vm, id string) error {
	if _, err := s.Get(vm, id); err != nil {
		return err
	}
	return os.RemoveAll(s.dir(vm, id))
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}
