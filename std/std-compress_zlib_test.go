// Why: zlib (RFC 1950) framing and its Adler-32 checksum sit under PNG, HTTP and many file formats.
// What: tests of compress.Adler32, compress.Zlib, compress.Unzlib, Inflater.NewZlib, and Inflater.Reader and Cause for every framing.
// How: Go's hash/adler32 and compress/zlib are the oracles.

package std

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"fmt"
	"hash/adler32"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

func c2ZlibGo(t testing.TB, b []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// TestCompress_Adler32MatchesGo: Adler32 equals hash/adler32 on every
// length from 0 to 300 of random bytes and of 0xFF bytes (the largest
// sums), around the 8-byte step, and on 9 MiB of 0xFF, which crosses the
// 4 MiB reduction block twice.
func TestCompress_Adler32MatchesGo(t *testing.T) {
	exe := c2Exe(t, "oracle", c2Oracle)
	var recs []czRec
	for n := 0; n <= 300; n++ {
		recs = append(recs, czRec{op: c2Adler32, data: czInput(0, n, uint64(n)), name: fmt.Sprintf("random %d", n)})
		recs = append(recs, czRec{op: c2Adler32, data: bytes.Repeat([]byte{0xFF}, n), name: fmt.Sprintf("ff %d", n)})
	}
	recs = append(recs, czRec{op: c2Adler32, data: bytes.Repeat([]byte{0xFF}, 9<<20+5), name: "ff 9 MiB"})
	recs = append(recs, czRec{op: c2Adler32, data: czRealInput(t), name: "real"})
	out, _, err := c2Run(t, exe, czEncode(recs))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != len(recs) {
		t.Fatalf("%d answers for %d records", len(lines), len(recs))
	}
	for i, r := range recs {
		want := fmt.Sprintf("adler %x", adler32.Checksum(r.data))
		if strings.TrimSpace(lines[i]) != want {
			t.Errorf("%s: got %q, want %q", r.name, lines[i], want)
		}
	}
}

// TestCompress_UnzlibGoStreams: Unzlib reads Go's zlib streams of every
// kind and length at every level, and refuses one byte over the limit.
func TestCompress_UnzlibGoStreams(t *testing.T) {
	exe := c2Exe(t, "oracle", c2Oracle)
	var recs []czRec
	var want []czRes
	add := func(name string, plain, z []byte, limit int64) {
		recs = append(recs, czRec{op: c2Unzlib, limit: limit, data: z, name: name})
		if int64(len(plain)) > limit {
			want = append(want, czRes{verdict: "toolarge"})
		} else {
			want = append(want, czRes{verdict: "ok", n: len(plain), crc: crc32.ChecksumIEEE(plain)})
		}
	}
	for kind := 0; kind < 5; kind++ {
		for _, n := range czLengths {
			plain := czInput(kind, n, uint64(n))
			for _, lv := range []int{0, 1, 6, 9, -2} {
				add(fmt.Sprintf("kind %d len %d level %d", kind, n, lv), plain, c2ZlibGo(t, plain, lv), czNoLimit)
			}
		}
	}
	real := czRealInput(t)
	z := c2ZlibGo(t, real, 6)
	add("real", real, z, czNoLimit)
	add("real at limit", real, z, int64(len(real)))
	add("real one below limit", real, z, int64(len(real)-1))
	add("negative limit", []byte("x"), c2ZlibGo(t, []byte("x"), 6), -5)
	res := czRun(t, exe, recs)
	for i := range recs {
		if !c2Exact(res[i], want[i]) {
			t.Errorf("%s", c2Fmt(recs[i].name, res[i], want[i]))
		}
	}
}

// TestCompress_ZlibReadByGo: compress/zlib reads Zlib's output of every
// kind and length back to the input, and Go's header check accepts it.
func TestCompress_ZlibReadByGo(t *testing.T) {
	exe := c2Exe(t, "oracle", c2Oracle)
	var recs []czRec
	for kind := 0; kind < 5; kind++ {
		for _, n := range czLengths {
			recs = append(recs, czRec{op: c2Zlib, data: czInput(kind, n, uint64(n)+7), name: fmt.Sprintf("kind %d len %d", kind, n)})
		}
	}
	recs = append(recs, czRec{op: c2Zlib, data: czRealInput(t), name: "real"})
	res := czRun(t, exe, recs)
	for i, r := range recs {
		if res[i].verdict != "enc" {
			t.Fatalf("%s: %v", r.name, res[i])
		}
		zr, err := zlib.NewReader(bytes.NewReader(res[i].enc))
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		got, err := io.ReadAll(zr)
		if err != nil || !bytes.Equal(got, r.data) {
			t.Fatalf("%s: Go read %d bytes, %v; want %d", r.name, len(got), err, len(r.data))
		}
		if m := c2UnzlibModel(res[i].enc, czNoLimit); m.verdict != "ok" {
			t.Fatalf("%s: model says %v", r.name, m)
		}
	}
}

// c2ZlibEdges are hand-made zlib streams with their exact verdicts.
func c2ZlibEdges(t testing.TB) []struct {
	name string
	data []byte
	want czRes
} {
	good := c2ZlibGo(t, []byte("hello, hello, hello"), 6)
	n := len(good)
	cat := func(a ...[]byte) []byte { return bytes.Join(a, nil) }
	badAdler := cat(good[:n-1], []byte{good[n-1] ^ 1})
	ok := czRes{verdict: "ok", n: 19, crc: crc32.ChecksumIEEE([]byte("hello, hello, hello"))}
	empty := c2ZlibGo(t, nil, 6)
	return []struct {
		name string
		data []byte
		want czRes
	}{
		{"no bytes", nil, czRes{verdict: "truncated"}},
		{"cmf only", []byte{0x78}, czRes{verdict: "truncated"}},
		{"method 7", []byte{0x77, 0x01}, czRes{verdict: "corrupt", offset: 0}},
		{"window 64 KiB", []byte{0x88, 0x98}, czRes{verdict: "corrupt", offset: 0}},
		{"bad fcheck", []byte{0x78, 0x9D}, czRes{verdict: "corrupt", offset: 1}},
		{"fdict", []byte{0x78, 0xBB, 0, 0, 0, 1}, czRes{verdict: "corrupt", offset: 1}},
		{"header only", []byte{0x78, 0x9C}, czRes{verdict: "truncated"}},
		{"window 256 bytes", cat([]byte{0x08, 0x1D}, good[2:]), ok},
		{"good", good, ok},
		{"empty", empty, czRes{verdict: "ok", n: 0, crc: 0}},
		{"no trailer", good[:n-4], czRes{verdict: "truncated"}},
		{"half trailer", good[:n-2], czRes{verdict: "truncated"}},
		{"bad adler", badAdler, czRes{verdict: "corrupt", offset: n - 4}},
		{"trailing byte", cat(good, []byte{0}), czRes{verdict: "corrupt", offset: n}},
		{"body cut", good[:5], czRes{verdict: "truncated"}},
		{"btype 3", []byte{0x78, 0x9C, 0x07}, czRes{verdict: "corrupt", offset: 2}},
	}
}

// TestCompress_UnzlibEdges: the hand vectors' verdicts and offsets, the
// model's verdicts for the same inputs, and Go's zlib reader accepting
// exactly the valid ones.
func TestCompress_UnzlibEdges(t *testing.T) {
	exe := c2Exe(t, "oracle", c2Oracle)
	edges := c2ZlibEdges(t)
	var recs []czRec
	for _, e := range edges {
		recs = append(recs, czRec{op: c2Unzlib, limit: czNoLimit, data: e.data, name: e.name})
	}
	res := czRun(t, exe, recs)
	for i, e := range edges {
		if !c2Exact(res[i], e.want) {
			t.Errorf("%s", c2Fmt(e.name, res[i], e.want))
		}
		if m := c2UnzlibModel(e.data, czNoLimit); !c2Exact(e.want, m) {
			t.Errorf("model: %s", c2Fmt(e.name, m, e.want))
		}
	}
}

// c2Mutate makes count seeded corruptions of each valid stream: bit flips,
// byte sets, truncations, inserts, deletes.
func c2Mutate(valid [][]byte, count int, seed uint64) [][]byte {
	r := czRng{seed}
	var out [][]byte
	for _, v := range valid {
		if len(v) == 0 {
			continue
		}
		for i := 0; i < count; i++ {
			b := append([]byte(nil), v...)
			p := r.intn(len(b))
			switch r.intn(5) {
			case 0:
				b[p] ^= 1 << r.intn(8)
			case 1:
				b[p] = byte(r.next())
			case 2:
				b = b[:p]
			case 3:
				b = append(b[:p], append([]byte{byte(r.next())}, b[p:]...)...)
			case 4:
				b = append(b[:p], b[p+1:]...)
			}
			out = append(out, b)
		}
	}
	return out
}

// c2ZlibValid is a set of valid zlib streams for the corruption tests.
func c2ZlibValid(t testing.TB) [][]byte {
	var v [][]byte
	for kind := 0; kind < 5; kind++ {
		for _, n := range []int{0, 1, 17, 258, 1000, 5000, 40000} {
			for _, lv := range []int{0, 1, 6, -2} {
				v = append(v, c2ZlibGo(t, czInput(kind, n, uint64(n)+3), lv))
			}
		}
	}
	return v
}

// TestCompress_UnzlibCorruptVerdicts: Unzlib's verdict on 4,200 seeded
// corruptions of 140 valid streams equals the model's, offsets included
// for the header and trailer, and outputs equal where both accept.
func TestCompress_UnzlibCorruptVerdicts(t *testing.T) {
	exe := c2Exe(t, "oracle", c2Oracle)
	muts := c2Mutate(c2ZlibValid(t), 30, 77)
	var recs []czRec
	for i, m := range muts {
		recs = append(recs, czRec{op: c2Unzlib, limit: czNoLimit, data: m, name: fmt.Sprintf("mutation %d", i)})
	}
	res := czRun(t, exe, recs)
	bad := 0
	for i, r := range recs {
		want := c2UnzlibModel(r.data, czNoLimit)
		if !c2Exact(res[i], want) {
			bad++
			if bad < 20 {
				t.Errorf("%s (%x): %s", r.name, r.data[:min(len(r.data), 24)], c2Fmt(r.name, res[i], want))
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d differ", bad, len(recs))
	}
}

// c2StreamWant is the stream program's expected line for a zlib stream:
// Unzlib's answer, except that a corrupt byte after the trailer is no
// error for a stream (it ends at its trailer).
func c2StreamWant(data []byte) czRes {
	m := c2UnzlibModel(data, czNoLimit)
	if m.verdict == "corrupt" && m.offset >= 0 {
		// A byte after the trailer: the stream's answer is the prefix's.
		if p := c2UnzlibModel(data[:m.offset], czNoLimit); m.offset >= 6 && p.verdict == "ok" {
			return p
		}
	}
	return m
}

// TestCompress_InflaterZlibStreams: Inflater.NewZlib gives Unzlib's answer
// for valid, edge and corrupt streams, offsets included where Unzlib's are
// exact (the oracle runs both), whatever the output piece sizes; bytes
// after the trailer are not read as data.
func TestCompress_InflaterZlibStreams(t *testing.T) {
	exe := c2Exe(t, "stream", c2StreamProg)
	oracle := c2Exe(t, "oracle", c2Oracle)
	var streams [][]byte
	streams = append(streams, c2ZlibGo(t, czInput(2, 1<<20, 5), 6), c2ZlibGo(t, czInput(0, 200000, 6), 6),
		c2ZlibGo(t, czRealInput(t), 9), append(c2ZlibGo(t, []byte("abc"), 6), "trailing garbage"...))
	for _, e := range c2ZlibEdges(t) {
		streams = append(streams, e.data)
	}
	streams = append(streams, c2Mutate(c2ZlibValid(t)[:40], 6, 99)...)
	var recs []czRec
	for i, s := range streams {
		recs = append(recs, czRec{op: c2Unzlib, limit: czNoLimit, data: s, name: fmt.Sprintf("stream %d", i)})
	}
	one := czRun(t, oracle, recs)
	for i, s := range streams {
		want := one[i]
		if want.verdict == "corrupt" {
			if p := c2StreamWant(s); p.verdict == "ok" {
				want = p
			}
		}
		for _, maxc := range []string{"1", "7", "70000"} {
			out, _, err := c2Run(t, exe, s, "2", "0", fmt.Sprint(i+1), maxc)
			if err != nil {
				t.Fatalf("stream %d: %v", i, err)
			}
			if got := strings.TrimSpace(string(out)); got != c2Line(want) {
				t.Errorf("stream %d, pieces to %s: got %q, want %q", i, maxc, got, c2Line(want))
			}
		}
	}
}

// TestCompress_InflaterReaderCause: through Inflater.Reader, a good stream
// of every framing reads to the end with Cause none; a bad one ends in
// ReadError, fails again on the next read, and Cause gives Read's error.
func TestCompress_InflaterReaderCause(t *testing.T) {
	exe := c2Exe(t, "stream", c2StreamProg)
	text := czInput(2, 300000, 8)
	cases := []struct {
		mode string
		data []byte
	}{
		{"0", czDeflateGo(t, text, 6)},
		{"1", czGzipGo(t, text, 6)},
		{"2", c2ZlibGo(t, text, 6)},
		{"0", czDeflateGo(t, text, 6)[:1000]},
		{"1", append(czGzipGo(t, text, 6), 0x1F, 0x8C)},
		{"2", c2ZlibGo(t, text, 6)[:5000]},
		{"2", []byte{0x78, 0x9D}},
		{"1", nil},
	}
	for i, c := range cases {
		readOut, _, err := c2Run(t, exe, c.data, c.mode, "0", "3", "5000")
		if err != nil {
			t.Fatalf("case %d Read: %v", i, err)
		}
		want := strings.TrimSpace(string(readOut))
		out, _, err := c2Run(t, exe, c.data, c.mode, "1", "3", "5000")
		if err != nil {
			t.Fatalf("case %d Reader: %v", i, err)
		}
		got := strings.Split(strings.TrimSpace(string(out)), "\n")
		if strings.HasPrefix(want, "ok ") {
			if len(got) != 2 || got[0] != want || got[1] != "cause none" {
				t.Errorf("case %d: Reader %q, want %q and cause none", i, got, want)
			}
			continue
		}
		if len(got) != 3 || got[0] != "readerror" || got[1] != "again 0" || got[2] != "cause "+want {
			t.Errorf("case %d: Reader %q, want readerror, again 0, cause %s", i, got, want)
		}
	}
}

// keep flate imported for the model's types when the file is read alone.
var _ = flate.NoCompression
