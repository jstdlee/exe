//go:build linux || darwin

package snap

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// dataRegions lists f's data regions with SEEK_DATA/SEEK_HOLE. A filesystem
// without hole reporting answers with one region for the whole file.
func dataRegions(f *os.File, size int64) ([]region, error) {
	var out []region
	for off := int64(0); off < size; {
		data, err := f.Seek(off, unix.SEEK_DATA)
		if errors.Is(err, syscall.ENXIO) {
			break // only a hole remains
		}
		if err != nil {
			return nil, err
		}
		hole, err := f.Seek(data, unix.SEEK_HOLE)
		if err != nil {
			return nil, err
		}
		hole = min(hole, size)
		if hole <= data {
			break
		}
		out = append(out, region{data, hole})
		off = hole
	}
	return out, nil
}

func markSparse(*os.File) {} // holes need no flag here
