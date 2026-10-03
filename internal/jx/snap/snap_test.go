package snap

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const mib = 1 << 20

// sparseDisk writes a 64 MiB file with three data islands and holes
// elsewhere, plus an explicitly written (allocated) run of zeros.
func sparseDisk(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(64 * mib); err != nil {
		t.Fatal(err)
	}
	write := func(off int64, data []byte) {
		if _, err := f.WriteAt(data, off); err != nil {
			t.Fatal(err)
		}
	}
	head := make([]byte, 4096)
	rand.Read(head)
	write(0, head)
	mid := make([]byte, mib)
	rand.Read(mid)
	write(10*mib, mid)
	write(20*mib, make([]byte, 4*mib)) // allocated zeros
	tail := make([]byte, 4096)
	rand.Read(tail)
	write(64*mib-4096, tail)
}

func sameContent(t *testing.T, a, b string) {
	t.Helper()
	x, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(x, y) {
		t.Fatalf("%s and %s differ (sizes %d, %d)", a, b, len(x), len(y))
	}
}

func usage(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return allocated(st)
}

func noReflink(t *testing.T) {
	old := reflinkFn
	reflinkFn = func(*os.File, string, string) error { return errors.ErrUnsupported }
	t.Cleanup(func() { reflinkFn = old })
}

func TestSparseCopyKeepsHoles(t *testing.T) {
	noReflink(t)
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "disk.raw"), filepath.Join(dir, "copy.raw")
	sparseDisk(t, src)
	method, err := Copy(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if method != "sparse" {
		t.Fatalf("method = %q, want sparse", method)
	}
	sameContent(t, src, dst)
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 64*mib {
		t.Fatalf("size = %d", st.Size())
	}
	if st.Mode().Perm() != 0o600 && runtime.GOOS != "windows" {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	if runtime.GOOS != "linux" {
		return
	}
	// The source holds ~5 MiB allocated (1 MiB data + 4 MiB written zeros
	// + two 4 KiB pages); the copy must hold only the data, not the zeros
	// and never the 64 MiB a dense copy would.
	srcUsed, dstUsed := usage(t, src), usage(t, dst)
	if dstUsed > 2*mib {
		t.Errorf("copy allocates %d bytes, want about 1 MiB (source %d)", dstUsed, srcUsed)
	}
	if dstUsed >= srcUsed {
		t.Errorf("copy allocates %d bytes, source %d: the zero run was copied", dstUsed, srcUsed)
	}
}

func TestDataRegions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "d")
	sparseDisk(t, p)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rs, err := dataRegions(f, 64*mib)
	if err != nil {
		t.Fatal(err)
	}
	var covered int64
	for _, r := range rs {
		if r.start < 0 || r.end > 64*mib || r.end <= r.start {
			t.Fatalf("bad region %+v", r)
		}
		covered += r.end - r.start
	}
	// Every data byte must be inside a region.
	for _, off := range []int64{0, 10 * mib, 11*mib - 1, 64*mib - 1} {
		in := false
		for _, r := range rs {
			in = in || off >= r.start && off < r.end
		}
		if !in {
			t.Errorf("offset %d is in no region %v", off, rs)
		}
	}
	if runtime.GOOS == "linux" && covered >= 64*mib {
		t.Logf("filesystem reports no holes (regions %v)", rs)
	}
}

func TestCopyCanceled(t *testing.T) {
	noReflink(t)
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "disk.raw"), filepath.Join(dir, "copy.raw")
	sparseDisk(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Copy(ctx, src, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a canceled copy left %s behind", dst)
	}
}

func TestCopyWithReflinkAttempt(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "disk.raw"), filepath.Join(dir, "copy.raw")
	sparseDisk(t, src)
	method, err := Copy(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("method on this filesystem: %s", method)
	sameContent(t, src, dst)
	if _, err := Copy(context.Background(), src, dst); err == nil {
		t.Fatal("Copy over an existing file succeeded")
	}
}

func TestStore(t *testing.T) {
	noReflink(t)
	state := t.TempDir()
	disk := DiskPath(state, "box")
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	sparseDisk(t, disk)
	if err := os.Chmod(disk, 0o640); err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Root: filepath.Join(state, "jx", "snapshots")}
	ctx := context.Background()

	if list, err := s.List("box"); err != nil || len(list) != 0 {
		t.Fatalf("List on empty = %v, %v", list, err)
	}
	m1, err := s.Create(ctx, "box", disk, "clean")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(m1.ID) || m1.Label != "clean" || m1.Size != 64*mib || m1.Method != "sparse" || m1.VM != "box" {
		t.Fatalf("meta = %+v", m1)
	}
	if st, err := os.Stat(filepath.Join(s.Root, "box", m1.ID)); err != nil || (st.Mode().Perm() != 0o700 && runtime.GOOS != "windows") {
		t.Fatalf("snapshot dir: %v, %v", st, err)
	}

	// change the disk, take a second snapshot
	f, err := os.OpenFile(disk, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("changed"), 30*mib)
	f.Close()
	m2, err := s.Create(ctx, "box", disk, "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List("box")
	if err != nil || len(list) != 2 || list[0].ID != m2.ID || list[1].ID != m1.ID {
		t.Fatalf("List = %+v, %v; want newest first", list, err)
	}

	if err := s.Restore(ctx, "box", m1.ID, disk); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(disk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatal("restored disk differs from the snapshot")
	}
	if st, _ := os.Stat(disk); runtime.GOOS != "windows" && st.Mode().Perm() != 0o640 {
		t.Errorf("restored disk mode = %v, want 0640", st.Mode().Perm())
	}
	if _, err := os.Stat(disk + ".jx-restore"); !errors.Is(err, os.ErrNotExist) {
		t.Error("restore left its temp file")
	}

	for _, id := range []string{"../../etc", "nope", ""} {
		if err := s.Restore(ctx, "box", id, disk); !errors.Is(err, ErrNotFound) {
			t.Errorf("Restore(%q) = %v, want ErrNotFound", id, err)
		}
		if err := s.Delete("box", id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete(%q) = %v, want ErrNotFound", id, err)
		}
	}
	if err := s.Delete("box", m2.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List("box"); len(list) != 1 || list[0].ID != m1.ID {
		t.Fatalf("after delete List = %+v", list)
	}
	if _, err := s.Create(ctx, "box", filepath.Join(state, "missing.raw"), ""); err == nil {
		t.Fatal("Create from a missing disk succeeded")
	}
}
