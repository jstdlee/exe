//go:build !windows

package server

import "syscall"

// diskFree reports the bytes available to the daemon on path's filesystem.
func diskFree(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
