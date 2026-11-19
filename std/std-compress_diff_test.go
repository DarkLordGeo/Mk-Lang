// Why: Mk's deflate and gzip must interoperate with an independent implementation in both directions.
// What: Inflate and Gunzip on Go's compress/flate and compress/gzip output at every level, and Go inflating
// Mk's Deflate and Gzip output.
// How: generated and real inputs, plus seeded corruptions whose verdicts must equal the Go model's.

package std

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// czGenerated is the generated decode set: every kind, length and level,
// as raw deflate and as gzip.
func czGenerated(t *testing.T) []czRec {
	var recs []czRec
	for kind := 0; kind < 5; kind++ {
		for _, n := range czLengths {
			b := czInput(kind, n, uint64(n))
			for _, lv := range czLevels {
				name := fmt.Sprintf("kind %d n %d level %d", kind, n, lv)
				recs = append(recs, czRec{op: czInflate, limit: czNoLimit, data: czDeflateGo(t, b, lv), name: "inflate " + name})
				if lv == flate.DefaultCompression || lv == flate.NoCompression || lv == flate.BestSpeed {
					recs = append(recs, czRec{op: czGunzip, limit: czNoLimit, data: czGzipGo(t, b, lv), name: "gunzip " + name})
				}
			}
		}
	}
	real := czRealInput(t)
	for _, lv := range czLevels {
		recs = append(recs, czRec{op: czInflate, limit: czNoLimit, data: czDeflateGo(t, real, lv), name: fmt.Sprintf("inflate real level %d", lv)})
		recs = append(recs, czRec{op: czGunzip, limit: czNoLimit, data: czGzipGo(t, real, lv), name: fmt.Sprintf("gunzip real level %d", lv)})
	}
	return recs
}

func czCheck(t *testing.T, recs []czRec, got []czRes) {
	t.Helper()
	bad := 0
	for i, r := range recs {
		want := czModel(r)
		if !czSame(got[i], want) {
			t.Errorf("%s (op %d, %d bytes: % x): got %v, want %v", r.name, r.op, len(r.data), r.data[:min(len(r.data), 48)], got[i], want)
			if bad++; bad == 10 {
				t.Fatal("stopping after 10 differences")
			}
		}
	}
}

// TestSTDCOMPRESS_InflateGoStreams: Mk's Inflate and Gunzip read what Go's
// compressors write, at every level, on every generated kind and length
// and on real text, in a dev build (every check on).
func TestSTDCOMPRESS_InflateGoStreams(t *testing.T) {
	exe := czOracleExe(t, "")
	recs := czGenerated(t)
	czCheck(t, recs, czRun(t, exe, recs))
}

// TestSTDCOMPRESS_InflateGoStreamsRelease is the same in a release build.
func TestSTDCOMPRESS_InflateGoStreamsRelease(t *testing.T) {
	exe := czOracleExe(t, "--release")
	recs := czGenerated(t)
	czCheck(t, recs, czRun(t, exe, recs))
}

// czVectors are hand-made streams for the edges of RFC 1951 and 1952,
// each with the verdict Mk must give (offsets included).
func czVectors(t *testing.T) []struct {
	rec  czRec
	want string
} {
	hello := []byte("hello")
	gz := czGzipGo(t, hello, 6)
	two := append(append([]byte{}, gz...), czGzipGo(t, []byte(" world"), 1)...)
	badCrc := append([]byte{}, gz...)
	badCrc[len(badCrc)-8] ^= 1
	badSize := append([]byte{}, gz...)
	badSize[len(badSize)-4] ^= 1
	// A header with FEXTRA, FNAME, FCOMMENT and FHCRC.
	hdr := []byte{0x1f, 0x8b, 8, 4 | 8 | 16 | 2, 0, 0, 0, 0, 0, 3, 3, 0, 'a', 'b', 'c', 'n', 'm', 0, 'c', 0}
	hc := crc32.ChecksumIEEE(hdr)
	full := append(append([]byte{}, hdr...), byte(hc), byte(hc>>8))
	full = append(full, gz[10:]...)
	badHcrc := append([]byte{}, full...)
	badHcrc[len(hdr)] ^= 1
	raw := czDeflateGo(t, hello, 6)
	v := []struct {
		rec  czRec
		want string
	}{
		{czRec{op: czInflate, data: []byte{1, 0, 0, 0xff, 0xff}}, "ok 0 0"},
		{czRec{op: czInflate, data: []byte{3, 0}}, "ok 0 0"},                                  // final fixed block, end code only
		{czRec{op: czInflate, data: []byte{1, 5, 0, 0xfa, 0xfe, 1, 2, 3, 4, 5}}, "corrupt 1"}, // NLEN is not ~LEN
		{czRec{op: czInflate, data: []byte{7}}, "corrupt 0"},                                  // BTYPE 3
		{czRec{op: czInflate, data: []byte{}}, "truncated"},
		{czRec{op: czInflate, data: []byte{1, 5, 0, 0xfa, 0xff, 1, 2}}, "truncated"},
		{czRec{op: czInflate, data: append(append([]byte{}, raw...), 0)}, fmt.Sprintf("corrupt %d", len(raw))},
		{czRec{op: czInflate, data: raw, limit: 5}, fmt.Sprintf("ok 5 %x", crc32.ChecksumIEEE(hello))},
		{czRec{op: czInflate, data: raw, limit: 4}, "toolarge"},
		{czRec{op: czInflate, data: raw, limit: -1}, "toolarge"},
		{czRec{op: czGunzip, data: gz}, fmt.Sprintf("ok 5 %x", crc32.ChecksumIEEE(hello))},
		{czRec{op: czGunzip, data: two}, fmt.Sprintf("ok 11 %x", crc32.ChecksumIEEE([]byte("hello world")))},
		{czRec{op: czGunzip, data: two, limit: 10}, "toolarge"},
		{czRec{op: czGunzip, data: badCrc}, fmt.Sprintf("corrupt %d", len(gz)-8)},
		{czRec{op: czGunzip, data: badSize}, fmt.Sprintf("corrupt %d", len(gz)-4)},
		{czRec{op: czGunzip, data: full}, fmt.Sprintf("ok 5 %x", crc32.ChecksumIEEE(hello))},
		{czRec{op: czGunzip, data: badHcrc}, fmt.Sprintf("corrupt %d", len(hdr))},
		{czRec{op: czGunzip, data: append(append([]byte{}, gz...), 0)}, fmt.Sprintf("corrupt %d", len(gz))},
		{czRec{op: czGunzip, data: append(append([]byte{}, gz...), 0x1f)}, "truncated"},
		{czRec{op: czGunzip, data: gz[:len(gz)-1]}, "truncated"},
		{czRec{op: czGunzip, data: []byte{0x1f, 0x8b, 7}}, "corrupt 2"},
		{czRec{op: czGunzip, data: []byte{0x1f, 0x8b, 8, 0x20, 0, 0, 0, 0, 0, 0}}, "corrupt 3"},
		{czRec{op: czGunzip, data: []byte{}}, "truncated"},
	}
	for i := range v {
		if v[i].rec.limit == 0 {
			v[i].rec.limit = czNoLimit
		}
		v[i].rec.name = fmt.Sprintf("vector %d", i)
	}
	return v
}

// TestSTDCOMPRESS_EdgeVectors: the hand-made edge streams give their
// verdicts, offsets included.
func TestSTDCOMPRESS_EdgeVectors(t *testing.T) {
	exe := czOracleExe(t, "")
	v := czVectors(t)
	recs := make([]czRec, len(v))
	for i := range v {
		recs[i] = v[i].rec
	}
	got := czRun(t, exe, recs)
	for i := range v {
		if got[i].String() != v[i].want {
			t.Errorf("vector %d (% x): got %v, want %s", i, v[i].rec.data[:min(len(v[i].rec.data), 16)], got[i], v[i].want)
		}
	}
}

// TestSTDCOMPRESS_GoReadsMkOutput: Go's compress/flate and compress/gzip
// read Mk's Deflate and Gzip output back to the input, on every generated
// kind and length and on real text.
func TestSTDCOMPRESS_GoReadsMkOutput(t *testing.T) {
	czGoReadsMk(t, czOracleExe(t, "--release"))
}

// TestSTDCOMPRESS_GoReadsMkOutputDev is the same in a dev build, where an
// overflow or a bounds slip in the encoder panics.
func TestSTDCOMPRESS_GoReadsMkOutputDev(t *testing.T) {
	czGoReadsMk(t, czOracleExe(t, ""))
}

func czGoReadsMk(t *testing.T, exe string) {
	var recs []czRec
	var inputs [][]byte
	for kind := 0; kind < 5; kind++ {
		for _, n := range czLengths {
			b := czInput(kind, n, uint64(n)+7)
			inputs = append(inputs, b, b)
			name := fmt.Sprintf("kind %d n %d", kind, n)
			recs = append(recs, czRec{op: czDeflate, data: b, name: "deflate " + name}, czRec{op: czGzip, data: b, name: "gzip " + name})
		}
	}
	real := czRealInput(t)
	inputs = append(inputs, real, real)
	recs = append(recs, czRec{op: czDeflate, data: real, name: "deflate real"}, czRec{op: czGzip, data: real, name: "gzip real"})
	got := czRun(t, exe, recs)
	for i, r := range recs {
		if got[i].verdict != "enc" {
			t.Fatalf("%s: %v", r.name, got[i])
		}
		var out []byte
		var err error
		if r.op == czDeflate {
			out, err = io.ReadAll(flate.NewReader(bytes.NewReader(got[i].enc)))
		} else {
			var zr *gzip.Reader
			zr, err = gzip.NewReader(bytes.NewReader(got[i].enc))
			if err == nil {
				out, err = io.ReadAll(zr)
			}
		}
		if err != nil || !bytes.Equal(out, inputs[i]) {
			t.Errorf("%s: Go read %d bytes, err %v; want the %d input bytes", r.name, len(out), err, len(inputs[i]))
		}
	}
}

// czMutate returns a seeded corruption of b: a bit flip, a byte set, a
// truncation, a byte inserted or deleted, or a run of bytes zeroed.
func czMutate(b []byte, r *czRng) []byte {
	c := append([]byte{}, b...)
	if len(c) == 0 {
		return []byte{byte(r.next())}
	}
	switch r.intn(6) {
	case 0:
		i := r.intn(len(c))
		c[i] ^= 1 << r.intn(8)
	case 1:
		c[r.intn(len(c))] = byte(r.next())
	case 2:
		c = c[:r.intn(len(c))]
	case 3:
		i := r.intn(len(c) + 1)
		c = append(c[:i], append([]byte{byte(r.next())}, c[i:]...)...)
	case 4:
		i := r.intn(len(c))
		c = append(c[:i], c[i+1:]...)
	case 5:
		i := r.intn(len(c))
		for k := i; k < len(c) && k < i+1+r.intn(8); k++ {
			c[k] = 0
		}
	}
	if r.intn(4) == 0 && len(c) > 0 {
		c[r.intn(len(c))] ^= 1 << r.intn(8)
	}
	return c
}

// czCorruptSet is n seeded corruptions of small valid streams (every
// kind, a few lengths and levels, raw and gzip).
func czCorruptSet(t *testing.T, seed uint64, n int) []czRec {
	var base []czRec
	for kind := 0; kind < 5; kind++ {
		for _, ln := range []int{0, 1, 5, 40, 300, 2000, 9000} {
			b := czInput(kind, ln, uint64(ln)+3)
			for _, lv := range []int{flate.NoCompression, flate.BestSpeed, flate.DefaultCompression, flate.HuffmanOnly} {
				base = append(base, czRec{op: czInflate, limit: czNoLimit, data: czDeflateGo(t, b, lv)})
			}
			base = append(base, czRec{op: czGunzip, limit: czNoLimit, data: czGzipGo(t, b, flate.DefaultCompression)})
		}
	}
	r := czRng{seed}
	recs := make([]czRec, 0, n)
	for i := 0; i < n; i++ {
		b := base[r.intn(len(base))]
		recs = append(recs, czRec{op: b.op, limit: czNoLimit, data: czMutate(b.data, &r), name: fmt.Sprintf("mutation %d", i)})
	}
	return recs
}

// TestSTDCOMPRESS_CorruptVerdicts: on seeded corruptions of valid streams,
// Mk's verdict (ok, corrupt, truncated) is the Go model's, and an accepted
// stream gives Go's output; in a dev build, so no corruption reaches a
// panic either.
func TestSTDCOMPRESS_CorruptVerdicts(t *testing.T) {
	exe := czOracleExe(t, "")
	recs := czCorruptSet(t, 1, 6000)
	czCheck(t, recs, czRun(t, exe, recs))
}

// TestSTDCOMPRESS_RatioAgainstGo: Deflate's one level compresses real and
// generated text to within 5 percent of Go's default level (6), the level
// whose algorithm it follows, and logs the sizes.
func TestSTDCOMPRESS_RatioAgainstGo(t *testing.T) {
	exe := czOracleExe(t, "--release")
	ins := map[string][]byte{
		"real":    czRealInput(t),
		"text":    czInput(2, 1<<20, 21),
		"pattern": czInput(4, 1<<20, 22),
		"runs":    czInput(3, 1<<20, 23),
		"random":  czInput(0, 1<<20, 24),
	}
	names := []string{"real", "text", "pattern", "runs", "random"}
	var recs []czRec
	for _, k := range names {
		recs = append(recs, czRec{op: czDeflate, data: ins[k], name: k})
	}
	got := czRun(t, exe, recs)
	for i, k := range names {
		mk := len(got[i].enc)
		g6 := len(czDeflateGo(t, ins[k], 6))
		g9 := len(czDeflateGo(t, ins[k], 9))
		t.Logf("%-8s %9d bytes: mk %9d, go-6 %9d, go-9 %9d (mk / go-6 %.4f)", k, len(ins[k]), mk, g6, g9, float64(mk)/float64(g6))
		if float64(mk) > 1.05*float64(g6) {
			t.Errorf("%s: Mk %d bytes, more than 1.05 of Go level 6's %d", k, mk, g6)
		}
	}
}

// TestSTDCOMPRESS_RebaseKeepsMatches: Compress's rebase of its u32 chains
// (every RebaseStep past RebaseAt) changes no output. The test builds the
// oracle over a std copy whose deflate.mk rebases every 64 KiB past 128
// KiB instead of every 1 GiB past 2 GiB, so a 1 MiB input rebases 14
// times; 64 KiB is more than the window plus a match, so every entry the
// rebase drops is out of reach in both builds, and the encodings must be
// byte-identical to the normal build's.
func TestSTDCOMPRESS_RebaseKeepsMatches(t *testing.T) {
	dir := t.TempDir()
	stdDir := czStdCopy(t, dir)
	p := filepath.Join(stdDir, "compress", "deflate.mk")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, r := range [][2]string{{"const RebaseAt = 2147483648", "const RebaseAt = 131072"}, {"const RebaseStep = 1073741824", "const RebaseStep = 65536"}} {
		if !strings.Contains(s, r[0]) {
			t.Fatalf("deflate.mk has no %q", r[0])
		}
		s = strings.Replace(s, r[0], r[1], 1)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	small := czBuildStd(t, dir, stdDir, czOracle, "--release")
	normal := czOracleExe(t, "--release")
	var recs []czRec
	for kind := 0; kind < 5; kind++ {
		for _, n := range []int{300000, 1 << 20} {
			recs = append(recs, czRec{op: czGzip, data: czInput(kind, n, uint64(n)+31), name: fmt.Sprintf("kind %d n %d", kind, n)})
		}
	}
	recs = append(recs, czRec{op: czDeflate, data: czRealInput(t), name: "real"})
	a := czRun(t, small, recs)
	w := czRun(t, normal, recs)
	for i, r := range recs {
		if !bytes.Equal(a[i].enc, w[i].enc) {
			t.Errorf("%s: rebasing build wrote %d bytes, normal build %d, and they differ", r.name, len(a[i].enc), len(w[i].enc))
		}
	}
}
