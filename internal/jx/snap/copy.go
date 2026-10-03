package snap

import (
	"bytes"
	"context"
	"io"
	"os"
)

// reflinkFn is replaced in tests to force the sparse path.
var reflinkFn = reflink

// Copy copies src to dst, which must not exist, as a reflink when the
// filesystem supports it, else as a sparse copy. It reports the method
// used: "reflink" or "sparse". dst is created 0600.
func Copy(ctx context.Context, src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", err
	}
	if err := reflinkFn(in, src, dst); err == nil {
		return "reflink", nil
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	err = sparseCopy(ctx, in, out, st.Size())
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	return "sparse", nil
}

// region is a byte range [start, end) of a file that may hold data.
type region struct{ start, end int64 }

// zeroChunk is the granularity of zero detection: an all-zero chunk inside
// a data region becomes a hole in the copy too.
const zeroChunk = 64 << 10

var zeros = make([]byte, zeroChunk)

// sparseCopy writes only src's data to dst: it visits the data regions the
// filesystem reports (SEEK_DATA/SEEK_HOLE where supported, else the whole
// file) and skips all-zero chunks inside them, then sets dst's size so any
// trailing hole stays a hole.
func sparseCopy(ctx context.Context, src, dst *os.File, size int64) error {
	markSparse(dst) // best effort: a non-sparse dst is still a correct copy
	regions, err := dataRegions(src, size)
	if err != nil {
		regions = []region{{0, size}}
	}
	buf := make([]byte, 1<<20)
	for _, r := range regions {
		for off := r.start; off < r.end; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := int64(len(buf))
			if rem := r.end - off; rem < n {
				n = rem
			}
			got, err := src.ReadAt(buf[:n], off)
			if int64(got) < n {
				if err == nil || err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			if err := writeNonZero(dst, buf[:n], off); err != nil {
				return err
			}
			off += n
		}
	}
	return dst.Truncate(size)
}

func writeNonZero(dst *os.File, data []byte, off int64) error {
	for len(data) > 0 {
		n := min(len(data), zeroChunk)
		if !bytes.Equal(data[:n], zeros[:n]) {
			if _, err := dst.WriteAt(data[:n], off); err != nil {
				return err
			}
		}
		data, off = data[n:], off+int64(n)
	}
	return nil
}
