package board

import (
	"bytes"
	"strings"
	"testing"
)

func TestPgidSnifferStripsMarkAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	p := &pgidSniffer{w: &out}
	for _, chunk := range []string{"\x1eexe-", "pgid 4", "321\n{\"type\"", ":\"x\"}\n"} {
		if n, err := p.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if p.PGID() != 4321 || out.String() != "{\"type\":\"x\"}\n" {
		t.Errorf("pgid %d, out %q", p.PGID(), out.String())
	}

	out.Reset()
	q := &pgidSniffer{w: &out}
	q.Write([]byte("plain output\n"))
	if q.PGID() != 0 || out.String() != "plain output\n" {
		t.Errorf("no mark: pgid %d, out %q", q.PGID(), out.String())
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	lw := NewLineWriter(func(b []byte) { got = append(got, string(b)) })
	lw.Write([]byte("a\r\nb"))
	lw.Write([]byte("c\n\nd"))
	lw.Flush()
	if strings.Join(got, "|") != "a|bc||d" {
		t.Errorf("lines = %q", got)
	}
}
