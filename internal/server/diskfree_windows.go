//go:build windows

package server

import "golang.org/x/sys/windows"

// diskFree reports the bytes available to the daemon on path's filesystem.
func diskFree(path string) int64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0
	}
	return int64(free)
}
