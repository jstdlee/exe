package snap

import (
	"os"

	"golang.org/x/sys/unix"
)

// reflink clones src into a new dst with FICLONE (btrfs, XFS, bcachefs,
// ...). It fails on filesystems without shared extents (ext4, tmpfs) and
// across filesystems; the caller then copies sparsely.
func reflink(in *os.File, _, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}
