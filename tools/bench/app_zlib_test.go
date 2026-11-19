// Why: bytes after a compressed stream would count in an output's size, so
// the round-trip check must refuse them.
// What: TestAppOutputSumTrailing.
// How: runs the app table's round-trip output check (outputSum, app.go) on
// outputs with bytes after the stream.

package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppOutputSumTrailing: a zlib or gzip output whose stream round-trips
// passes, and the same output with any byte after the stream fails, so a
// cell cannot pad its file and the compressed size counts the stream only.
func TestAppOutputSumTrailing(t *testing.T) {
	plain := bytes.Repeat([]byte("the round trip's input\n"), 1000)
	data := filepath.Join(t.TempDir(), "in.txt")
	writeFile(t, data, string(plain))
	var zb, gb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(plain)
	zw.Close()
	gw := gzip.NewWriter(&gb)
	gw.Write(plain)
	gw.Close()
	for _, c := range []struct {
		check string
		file  []byte
	}{{"zlib", zb.Bytes()}, {"gzip", gb.Bytes()}} {
		got, err := outputSum(c.check, c.file, data)
		if err != nil || got != "roundtrip sha256:"+sha256Hex(plain) {
			t.Fatalf("%s: the stream alone: %q, %v", c.check, got, err)
		}
		for _, tail := range [][]byte{{0}, []byte("x"), c.file[:4]} {
			padded := append(append([]byte(nil), c.file...), tail...)
			if got, err := outputSum(c.check, padded, data); err == nil {
				t.Errorf("%s: %d bytes after the stream passed as %q", c.check, len(tail), got)
			} else if c.check == "zlib" && !strings.Contains(err.Error(), "follow the zlib stream") {
				t.Errorf("zlib: refused, but not for the trailing bytes: %v", err)
			}
		}
	}
}
