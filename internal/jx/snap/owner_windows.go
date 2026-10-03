package snap

import "os"

func allocated(os.FileInfo) int64 { return 0 }

func chownLike(string, os.FileInfo) error { return nil }
