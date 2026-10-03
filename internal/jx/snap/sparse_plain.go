//go:build !linux && !darwin && !windows

package snap

import "os"

func markSparse(*os.File) {}
