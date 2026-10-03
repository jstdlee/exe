//go:build !linux && !darwin

package snap

import (
	"errors"
	"os"
)

// reflink is not available here (ReFS block cloning is not wired up); the
// caller copies sparsely.
func reflink(_ *os.File, _, _ string) error { return errors.ErrUnsupported }
