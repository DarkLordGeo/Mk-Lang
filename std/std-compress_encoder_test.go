// Why: compress.Encoder's output must be readable by other implementations at every point a caller flushes.
// What: tests of compress.Encoder with Go's compress/flate, compress/gzip and compress/zlib as readers.
// How: every stream reads back to its input whatever the write sizes and flushes, and every sync flush
// makes all input so far decodable from the output so far.

package std

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

// c2Decode reads an Encoder's whole output with Go's reader for framing f.
func c2Decode(f int, enc []byte) ([]byte, error) {
	var r io.Reader
	switch f {
	case 0:
		r = flate.NewReader(bytes.NewReader(enc))
	case 1:
		g, err := gzip.NewReader(bytes.NewReader(enc))
		if err != nil {
			return nil, err
		}
		g.Multistream(false)
		r = g
	case 2:
		z, err := zlib.NewReader(bytes.NewReader(enc))
		if err != nil {
			return nil, err
		}
		r = z
	}
	return io.ReadAll(r)
}

// c2Encode runs the Encoder program over data and returns its output and
// the input positions of its flushes.
func c2Encode(t *testing.T, exe string, data []byte, f, level int, seed uint64, maxp, chance, stop int) ([]byte, []int) {
	t.Helper()
	out, errs, err := c2Run(t, exe, czEncode([]czRec{{data: data}}), fmt.Sprint(f), fmt.Sprint(level), fmt.Sprint(seed),
		fmt.Sprint(maxp), fmt.Sprint(chance), fmt.Sprint(stop))
	if err != nil {
		t.Fatalf("encoder: %v\n%s", err, errs)
	}
	var flushes []int
	for _, l := range strings.Fields(strings.ReplaceAll(errs, "flush", "")) {
		p, err := strconv.Atoi(l)
		if err != nil {
			t.Fatalf("encoder stderr %q", errs)
		}
		flushes = append(flushes, p)
	}
	return out, flushes
}

// TestCompress_EncoderRoundTrip: every framing at levels 0 and 6, over
// every kind of input at lengths around the chunk (64 KiB), the window and
// the history buffer (96 KiB), written in pieces of 1 byte to 70,000, with
// and without flushes, is read back by Go to the input; the trailer is
// checked by Go's gzip and zlib readers.
func TestCompress_EncoderRoundTrip(t *testing.T) {
	exe := c2Exe(t, "enc", c2EncProg)
	lengths := []int{0, 1, 12, 13, 100, 32768, 65535, 65536, 65537, 98303, 98304, 98305, 200000, 1 << 20}
	n := 0
	for kind := 0; kind < 5; kind++ {
		for li, ln := range lengths {
			data := czInput(kind, ln, uint64(ln)+11)
			for f := 0; f < 3; f++ {
				for _, level := range []int{0, 6} {
					maxp := []int{70000, 7, 4096, 65536, 100000}[(kind+li+f)%5]
					if maxp == 7 && ln > 100000 {
						maxp = 70000
					}
					chance := []int{0, 3, 50}[(li+f+level)%3]
					seed := uint64(kind*1000 + li*10 + f)
					enc, _ := c2Encode(t, exe, data, f, level, seed, maxp, chance, 0)
					got, err := c2Decode(f, enc)
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("kind %d len %d framing %d level %d pieces %d flush 1/%d: Go read %d bytes, %v", kind, ln, f, level, maxp, chance, len(got), err)
					}
					n++
				}
			}
		}
	}
	real := czRealInput(t)
	for f := 0; f < 3; f++ {
		enc, _ := c2Encode(t, exe, real, f, 6, 5, 70000, 0, 0)
		got, err := c2Decode(f, enc)
		if err != nil || !bytes.Equal(got, real) {
			t.Fatalf("real, framing %d: Go read %d bytes, %v", f, len(got), err)
		}
		if f == 0 {
			goSize := len(czDeflateGo(t, real, 6))
			t.Logf("real text %d bytes: Encoder %d, Go level 6 %d (%.4f)", len(real), len(enc), goSize, float64(len(enc))/float64(goSize))
			if float64(len(enc)) > 1.05*float64(goSize) {
				t.Errorf("Encoder output %d over 1.05 of Go level 6's %d", len(enc), goSize)
			}
		}
	}
	t.Logf("%d streams", n)
}

// TestCompress_EncoderSyncFlush: for each flush of a seeded run, the
// output up to that flush (the run stopped and abandoned there) decodes in
// Go to exactly the input written before it, and ends byte-aligned with
// the empty stored block 00 00 FF FF.
func TestCompress_EncoderSyncFlush(t *testing.T) {
	exe := c2Exe(t, "enc", c2EncProg)
	data := czInput(2, 300000, 21)
	for f := 0; f < 3; f++ {
		for _, level := range []int{0, 6} {
			_, flushes := c2Encode(t, exe, data, f, level, 9, 9000, 8, 0)
			if len(flushes) < 3 {
				t.Fatalf("framing %d: only %d flushes", f, len(flushes))
			}
			for k, pos := range flushes {
				if k > 6 {
					break
				}
				enc, fl := c2Encode(t, exe, data, f, level, 9, 9000, 8, k+1)
				if len(fl) != k+1 || fl[k] != pos {
					t.Fatalf("framing %d stop %d: flushes %v, want %v", f, k+1, fl, flushes[:k+1])
				}
				if !bytes.HasSuffix(enc, []byte{0, 0, 0xFF, 0xFF}) {
					t.Fatalf("framing %d flush %d: output does not end with the sync marker", f, k)
				}
				body := enc
				if f == 1 {
					body = enc[10:]
				} else if f == 2 {
					body = enc[2:]
				}
				got, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
				if !errors.Is(err, io.ErrUnexpectedEOF) || !bytes.Equal(got, data[:pos]) {
					t.Fatalf("framing %d level %d flush %d at %d: Go read %d bytes, %v", f, level, k, pos, len(got), err)
				}
			}
		}
	}
}
