package snap

import (
	"os"

	"golang.org/x/sys/windows"
)

// fsctlSetSparse marks a file sparse (x/sys/windows has no constant for it).
const fsctlSetSparse = 0x000900C4

// markSparse flags dst as an NTFS sparse file, so ranges never written
// (the zero chunks sparseCopy skips) stay unallocated.
func markSparse(f *os.File) {
	var ret uint32
	_ = windows.DeviceIoControl(windows.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &ret, nil)
}
