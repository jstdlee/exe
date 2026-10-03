//go:build !windows

package snap

import (
	"os"
	"syscall"
)

// allocated is the space a file occupies on the host: its allocated
// blocks, not its apparent size.
func allocated(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return 0
}

// chownLike gives path the owner of fi (a root daemon restoring a disk
// that belongs to the VM's unprivileged runner).
func chownLike(path string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) == os.Getuid() && int(st.Gid) == os.Getgid() {
		return nil
	}
	return os.Chown(path, int(st.Uid), int(st.Gid))
}
