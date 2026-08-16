//go:build windows

package hoststats

import "golang.org/x/sys/windows"

func DiskUsage(path string) (free, total int64) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var freeB, totalB uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeB, &totalB, nil); err != nil {
		return 0, 0
	}
	return int64(freeB), int64(totalB)
}
