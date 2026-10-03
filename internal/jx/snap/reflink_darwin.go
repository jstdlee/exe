package snap

import (
	"os"

	"golang.org/x/sys/unix"
)

// reflink clones src to dst with clonefile(2) (APFS). It fails on other
// filesystems and across volumes; the caller then copies sparsely.
func reflink(_ *os.File, src, dst string) error {
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		return err
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
