// Why: more compress.ZstdDecoder tests, split from std-compress_zstd_test.go to keep each file under 1,200 lines.
// What: tests of the checksum's streaming XXH64 and of ZstdContent.
// How: the helpers (c2ZExe, czRec, czEncode, czInput, c2ZManifest, c2Exe, c2Run, c2ContentProg) are in
// std-compress_zstd_test.go and its siblings.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestCompress_ZstdXx64: the checksum's streaming XXH64 equals hash.Xx64
// over every kind and length, absorbed in pieces of 1 to 100 bytes.
func TestCompress_ZstdXx64(t *testing.T) {
	exe := c2ZExe(t)
	var recs []czRec
	for kind := 0; kind < 5; kind++ {
		for _, n := range []int{0, 1, 3, 4, 7, 8, 31, 32, 33, 63, 64, 65, 100, 1000, 70000} {
			for s := 0; s < 3; s++ {
				recs = append(recs, czRec{op: 2, limit: int64(s*31 + n), data: czInput(kind, n, uint64(n)), name: fmt.Sprintf("k%d n%d", kind, n)})
			}
		}
	}
	ctxOut := czRunXX(t, exe, recs)
	for i, l := range ctxOut {
		if l != "xx ok" {
			t.Errorf("%s: %q", recs[i].name, l)
		}
	}
}

// czRunXX runs the batch program over xx records and returns its lines.
func czRunXX(t *testing.T, exe string, recs []czRec) []string {
	cmd := exec.Command(exe, "1", "1", "0")
	cmd.Stdin = bytes.NewReader(czEncode(recs))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("xx: %v", err)
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != len(recs) {
		t.Fatalf("xx: %d lines for %d records", len(lines), len(recs))
	}
	return lines
}

// TestCompress_ZstdContent: ZstdContent's name is zstd, and its wrap,
// called as an HTTP client calls it (c2ContentProg with ZstdContent),
// reads the stored frames to their output and fails with ReadError, again
// on the next read, on a truncated or corrupt one.
func TestCompress_ZstdContent(t *testing.T) {
	const line = `c := if sys.args[1] == "gzip" { compress.GzipContent } else { compress.DeflateContent }`
	if !strings.Contains(c2ContentProg, line) {
		t.Fatalf("c2ContentProg has no %q", line)
	}
	exe := c2Exe(t, "zcontent", strings.Replace(c2ContentProg, line, "c := compress.ZstdContent", 1))
	ents, frames := c2ZManifest(t)
	n := 0
	for i, e := range ents {
		if e.n > 5000 && !strings.HasPrefix(e.name, "multi") && !strings.HasPrefix(e.name, "real") {
			continue
		}
		piece := []int{1, 7, 4096, 65536}[i%4]
		for _, c := range []struct {
			in   []byte
			want string
		}{
			{frames[i], fmt.Sprintf("ok %d %x\n", e.n, e.crc)},
			{frames[i][:len(frames[i])-1], "readerror\nagain 0\n"},
			{append([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x08}, frames[i][5:]...), "readerror\nagain 0\n"},
		} {
			out, errs, err := c2Run(t, exe, c.in, "zstd", fmt.Sprint(piece))
			if err != nil {
				t.Fatalf("%s: %v\n%s", e.name, err, errs)
			}
			if string(out) != "name zstd\n"+c.want {
				t.Errorf("%s (%d bytes, pieces of %d): got %q, want %q", e.name, len(c.in), piece, out, c.want)
			}
			n++
		}
	}
	t.Logf("%d streams", n)
}
