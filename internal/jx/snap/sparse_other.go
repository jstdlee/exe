//go:build !linux && !darwin

package snap

import "os"

// dataRegions has no hole map to read here: the whole file is one region,
// and sparseCopy's zero-chunk skipping finds the holes.
func dataRegions(_ *os.File, size int64) ([]region, error) {
	return []region{{0, size}}, nil
}
