// Why: an HTTP client decodes gzip and deflate bodies through compress.Content.
// What: GzipContent's and DeflateContent's wrap, called as an HTTP client calls it, over a stdin body Reader.
// How: reads Go's compress/gzip and compress/zlib streams back to their input, and on a bad stream fails
// with ReadError, again on the next read.

package std

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
)

// c2ContentProg reads stdin through the Content named by args[1] (gzip or
// deflate) in pieces of at most args[2] bytes, and prints "ok <len> <crc32
// hex>" or "readerror" and whether the next read fails too ("again 0").
const c2ContentProg = `import std.compress
import std.hash

Body(a: &Arena into a, c: compress.Content, src: mut Reader, out: mut Writer, piece: int): WriteError|OutOfMemory!void {
    var rb: [512]u8 = [0; 512]
    var r := try c.wrap(a, src, mut rb[..])
    buf := try a.Slice[u8](piece)
    var h := hash.Crc32State.New()
    var total := 0
    for {
        got := r.Read(mut buf[..]) else {
            try Print(out, "readerror\n")
            again := r.Read(mut buf[..]) else {
                0
            }
            try Print(out, "again {again}\n", (again: again))
            return
        }
        if got == 0 {
            break
        }
        h.Bytes(buf[0..got])
        total += got
    }
    sum := h.Sum()
    try Print(out, "ok {total} {sum:x}\n", (total: total, sum: sum))
}

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    piece := Cast[int](try sys.args[2].ParseInt())
    c := if sys.args[1] == "gzip" { compress.GzipContent } else { compress.DeflateContent }
    var a := try Arena.New(sys.heap)
    defer a.Free()
    try Print(mut out, "name {n}\n", (n: c.name))
    try Body(&a, c, mut inp, mut out, piece)
    try out.Flush()
}
`

// TestCompress_ContentWrap: each Content's name is its RFC 9110 token;
// its wrap decodes Go's streams of every kind and length, read in pieces
// of 1 byte to 64 KiB, to the input (gzip with two members back to back
// too); a bad stream (a raw DEFLATE stream sent as deflate, a gzip stream
// with a bad CRC-32, a truncated stream) fails with ReadError and fails
// again on the next read.
func TestCompress_ContentWrap(t *testing.T) {
	exe := c2Exe(t, "content", c2ContentProg)
	gz := func(b []byte) []byte {
		var o bytes.Buffer
		w := gzip.NewWriter(&o)
		w.Write(b)
		w.Close()
		return o.Bytes()
	}
	zl := func(b []byte) []byte {
		var o bytes.Buffer
		w := zlib.NewWriter(&o)
		w.Write(b)
		w.Close()
		return o.Bytes()
	}
	ok := func(b []byte) string {
		return fmt.Sprintf("ok %d %x\n", len(b), crc32.ChecksumIEEE(b))
	}
	type c2Case struct {
		name, coding string
		in           []byte
		want         string // the output's prefix after the name line
	}
	var cases []c2Case
	for kind := 0; kind < 5; kind++ {
		for _, n := range []int{0, 1, 1000, 70000, 300000} {
			data := czInput(kind, n, uint64(kind*7+n))
			cases = append(cases,
				c2Case{fmt.Sprintf("gzip kind %d n %d", kind, n), "gzip", gz(data), ok(data)},
				c2Case{fmt.Sprintf("deflate kind %d n %d", kind, n), "deflate", zl(data), ok(data)})
		}
	}
	a, b := czInput(1, 5000, 1), czInput(2, 9000, 2)
	cases = append(cases, c2Case{"gzip two members", "gzip", append(gz(a), gz(b)...), ok(append(append([]byte{}, a...), b...))})
	bad := gz(a)
	bad[len(bad)-6] ^= 1
	cases = append(cases,
		c2Case{"gzip bad crc", "gzip", bad, "readerror\nagain 0\n"},
		c2Case{"gzip truncated", "gzip", gz(a)[:len(gz(a))/2], "readerror\nagain 0\n"},
		c2Case{"deflate raw stream", "deflate", czDeflateGo(t, a, 6), "readerror\nagain 0\n"},
		c2Case{"deflate truncated", "deflate", zl(a)[:len(zl(a))/2], "readerror\nagain 0\n"},
		c2Case{"deflate empty body", "deflate", nil, "readerror\nagain 0\n"})
	for i, c := range cases {
		piece := []int{1, 7, 4096, 65536}[i%4]
		out, errs, err := c2Run(t, exe, c.in, c.coding, fmt.Sprint(piece))
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.name, err, errs)
		}
		got := string(out)
		head := "name " + c.coding + "\n"
		if !strings.HasPrefix(got, head) || got[len(head):] != c.want {
			t.Errorf("%s (pieces of %d): got %q, want %q after %q", c.name, piece, got, c.want, head)
		}
	}
}
