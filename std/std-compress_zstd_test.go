// Why: compress.ZstdDecoder reads untrusted frames and must agree with the reference decoder on every verdict.
// What: its tests over frames libzstd 1.5.7 wrote (std/testdata/zstd, with lengths and CRC-32s), the zstd golden
// files and Go's test frames in Mk-evidence, and Go's internal/zstd (through debug/elf) but for c2ZstdDiffers.
// How: a batch program over a std copy with the PieceReader shim (std-compress_pieces_test.go) reads each frame in
// seeded input and output pieces. Part of the cuttable zstd unit: delete with std/compress/zstd*.mk.
//
//	MK_ZGEN=<zgen.exe> go test ./std -run TestCompress_ZstdGenCorpus
//	go test ./std -run '^$' -fuzz FuzzCompress_Zstd -fuzztime 5m -parallel 2
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// c2ZxShim is a second test file of the std copy: ZxSplit absorbs data into
// the zstd checksum's streaming XXH64 in seeded pieces and returns the
// digest, for the test against hash.Xx64.
const c2ZxShim = `// Test shim (std-compress_zstd_test.go), not part of std.

export ZxSplit(data: []u8, seed: u64, maxp: int): u64 {
    var x := ZxNew()
    var s := seed
    var i := 0
    n := data.Len()
    for i < n {
        s = s +% 0x9E3779B97F4A7C15
        var k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxp))
        if k > n - i {
            k = n - i
        }
        ZxUpdate(mut x, data[i..i + k])
        i += k
    }
    return ZxDigest(&x)
}
`

// c2ZProg reads czEncode records from stdin: op 0 decodes through Read, 1
// through the Reader, 2 answers "xx ok" when ZxSplit agrees with hash.Xx64
// over pieces of up to 1 + seed % 100 bytes, 3 and 4 decode through Read
// and ReaderLazy of a ZstdDecoder.NewLazy; the limit field is the
// seed.
// Args: largest input piece, largest output piece, window_limit. Answers
// czParse's lines; the Reader's failure answers its Cause (after a second
// read fails too; "notsticky" else).
const c2ZProg = `import std.compress
import std.hash

Word(b: []u8, i: int, k: int): int {
    var x := 0
    for j in 0..k {
        x = x | (int(b[i + j]) << (8 * j))
    }
    return x
}

Verdict(out: mut Writer, e: compress.Error): WriteError!void {
    match e {
        compress.Corrupt(offset: o) {
            try Print(out, "corrupt {o}\n", (o: o))
        }
        compress.Truncated {
            try Print(out, "truncated\n")
        }
        compress.TooLarge {
            try Print(out, "toolarge\n")
        }
        OutOfMemory {
            try Print(out, "oom\n")
        }
        _ {
            Panic("compress.Error gained a member this match does not handle")
        }
    }
}

Stream(out: mut Writer, a: &Arena, io: Io, form: u8, data: []u8, seed: u64, maxp: int, maxo: int, wl: int): WriteError|OutOfMemory!void {
    if form == 2 {
        got := compress.ZxSplit(data, seed, 1 + Trunc[int](seed % 100))
        want := hash.Xx64(data, 0)
        if got == want {
            try Print(out, "xx ok\n")
        } else {
            try Print(out, "xx bad\n")
        }
        return
    }
    var src := try compress.PieceReader(a, io, data, seed, maxp)
    var z := if form >= 3 { compress.ZstdDecoder.NewLazy(a, wl) } else { compress.ZstdDecoder.New(a, wl) }
    buf := try a.Slice[u8](maxo)
    var s := seed ^ 0x2545F4914F6CDD1D
    var h := hash.Crc32State.New()
    var total := 0
    if form == 1 || form == 4 {
        var rb: [300]u8 = [0; 300]
        var r := if form == 4 { z.ReaderLazy(mut src, mut rb[..]) } else { z.Reader(mut src, mut rb[..]) }
        var failed := false
        for {
            s = s +% 0x9E3779B97F4A7C15
            k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxo))
            got := r.Read(mut buf[0..k]) else {
                again := r.Read(mut buf[0..k]) else {
                    -1
                }
                if again != -1 {
                    try Print(out, "notsticky\n")
                    return
                }
                failed = true
                break
            }
            if got == 0 {
                break
            }
            h.Bytes(buf[0..got])
            total += got
        }
        if failed {
            if e := z.Cause() {
                try Verdict(out, e)
            } else {
                try Print(out, "readerror\n")
            }
            return
        }
        c := h.Sum()
        try Print(out, "ok {total} {c:x}\n", (total: total, c: c))
        return
    }
    for {
        s = s +% 0x9E3779B97F4A7C15
        k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxo))
        got := z.Read(mut src, mut buf[0..k]) else e {
            match e {
                compress.Corrupt(offset: o) {
                    try Print(out, "corrupt {o}\n", (o: o))
                }
                compress.Truncated {
                    try Print(out, "truncated\n")
                }
                compress.TooLarge {
                    try Print(out, "toolarge\n")
                }
                OutOfMemory {
                    try Print(out, "oom\n")
                }
                ReadError {
                    try Print(out, "readerror\n")
                }
                _ {
                    Panic("compress.Error gained a member this match does not handle")
                }
            }
            return
        }
        if got == 0 {
            break
        }
        if got > k {
            try Print(out, "overrun\n")
            return
        }
        h.Bytes(buf[0..got])
        total += got
    }
    c := h.Sum()
    try Print(out, "ok {total} {c:x}\n", (total: total, c: c))
}

// Main names on stderr the error Batch returns: an error of the program
// itself, not a decoder's verdict (those are printed as answers).
export Main(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    Batch(sys) else e {
        var errw := sys.Stderr()
        match e {
            WriteError {
                try Print(mut errw, "batch: WriteError\n")
            }
            ReadError {
                try Print(mut errw, "batch: ReadError\n")
            }
            OutOfMemory {
                try Print(mut errw, "batch: OutOfMemory\n")
            }
            ParseError {
                try Print(mut errw, "batch: ParseError\n")
            }
        }
        try errw.Flush()
        fail e
    }
}

Batch(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    maxp := Cast[int](try sys.args[1].ParseInt())
    maxo := Cast[int](try sys.args[2].ParseInt())
    wl := Cast[int](try sys.args[3].ParseInt())
    var hdr: [13]u8 = [0; 13]
    var a := try Arena.New(sys.heap)
    defer a.Free()
    for {
        got := try inp.ReadAll(mut hdr[..])
        if got < 13 {
            return
        }
        n := Word(hdr[..], 9, 4)
        data := try a.Slice[u8](n)
        _ = try inp.ReadAll(data)
        try Stream(mut out, &a, sys.io, hdr[0], data[..], Cast[u64](Word(hdr[..], 1, 7)), maxp, maxo, wl)
        try out.Flush()
        a.Reset()
    }
}
`

// c2ZExe builds c2ZProg over a std copy with the PieceReader and ZxSplit
// shims, once per test binary and flags.
func c2ZExe(t testing.TB, flags ...string) string {
	t.Helper()
	return c2PieceExe(t, "zstd", c2ZProg, map[string]string{"testzx.mk": c2ZxShim}, flags...)
}

// c2ZAsk decodes each frame with the batch program: form 0 Read, 1 Reader,
// 3 a NewLazy decoder's Read, 4 its ReaderLazy; input pieces up to maxp,
// output pieces up to maxo, the window limit wl.
func c2ZAsk(t *testing.T, exe string, frames [][]byte, form byte, seed uint64, maxp, maxo, wl int) []czRes {
	t.Helper()
	recs := make([]czRec, len(frames))
	for i, f := range frames {
		recs[i] = czRec{op: form, limit: int64(seed + uint64(i)*7919), data: f, name: fmt.Sprint(i)}
	}
	return czRunArgs(t, exe, recs, fmt.Sprint(maxp), fmt.Sprint(maxo), fmt.Sprint(wl))
}

// c2ZstdGo decodes data with Go's zstd decoder: the bytes of an ELF file
// whose one section is SHF_COMPRESSED with ELFCOMPRESS_ZSTD, read by
// debug/elf, which hands the section's bytes to internal/zstd's Reader.
func c2ZstdGo(data []byte) ([]byte, error) {
	var f bytes.Buffer
	le := binary.LittleEndian
	strtab := []byte("\x00.z\x00.shstrtab\x00")
	secOff := 64
	secLen := 24 + len(data)
	strOff := secOff + secLen
	shOff := (strOff + len(strtab) + 7) &^ 7
	h := make([]byte, 64)
	copy(h, "\x7fELF\x02\x01\x01")
	le.PutUint16(h[16:], 1)  // ET_REL
	le.PutUint16(h[18:], 62) // EM_X86_64
	le.PutUint32(h[20:], 1)
	le.PutUint64(h[40:], uint64(shOff))
	le.PutUint16(h[52:], 64)
	le.PutUint16(h[58:], 64)
	le.PutUint16(h[60:], 3)
	le.PutUint16(h[62:], 2)
	f.Write(h)
	ch := make([]byte, 24)
	le.PutUint32(ch[0:], 2) // ELFCOMPRESS_ZSTD
	le.PutUint64(ch[8:], 1<<40)
	le.PutUint64(ch[16:], 1)
	f.Write(ch)
	f.Write(data)
	f.Write(strtab)
	for f.Len() < shOff {
		f.WriteByte(0)
	}
	sh := func(name, typ uint32, flags uint64, off, size int) {
		s := make([]byte, 64)
		le.PutUint32(s[0:], name)
		le.PutUint32(s[4:], typ)
		le.PutUint64(s[8:], flags)
		le.PutUint64(s[24:], uint64(off))
		le.PutUint64(s[32:], uint64(size))
		le.PutUint64(s[48:], 1)
		f.Write(s)
	}
	sh(0, 0, 0, 0, 0)
	sh(1, 1, 0x800, secOff, secLen) // SHT_PROGBITS, SHF_COMPRESSED
	sh(4, 3, 0, strOff, len(strtab))
	ef, err := elf.NewFile(bytes.NewReader(f.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("elf: %v", err)
	}
	return io.ReadAll(ef.Sections[1].Open())
}

// c2ZstdGoRes is Go's decoder's verdict on data: ok with the output's
// length and CRC-32, truncated for an unexpected end, else corrupt with
// the offset unknown (-1).
func c2ZstdGoRes(data []byte) czRes {
	out, err := c2ZstdGo(data)
	if err == nil {
		return czRes{verdict: "ok", n: len(out), crc: crc32.ChecksumIEEE(out)}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return czRes{verdict: "truncated"}
	}
	return czRes{verdict: "corrupt", offset: -1}
}

// c2ZEntry is one stored frame: its file, decoded length and CRC-32, and
// the smallest window limit it decodes under (its window), 0 when only
// the default is tested.
type c2ZEntry struct {
	name  string
	n     int
	crc   uint32
	limit int
}

const c2ZDir = "testdata/zstd"

// c2ZManifest reads std/testdata/zstd/MANIFEST and the frames it names.
func c2ZManifest(t testing.TB) ([]c2ZEntry, [][]byte) {
	b, err := os.ReadFile(filepath.Join(c2ZDir, "MANIFEST"))
	if err != nil {
		t.Fatalf("zstd corpus: %v (generate it with MK_ZGEN, see TestCompress_ZstdGenCorpus)", err)
	}
	var ents []c2ZEntry
	var frames [][]byte
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) != 4 {
			t.Fatalf("MANIFEST line %q", l)
		}
		n, _ := strconv.Atoi(f[1])
		c, _ := strconv.ParseUint(f[2], 16, 32)
		lim, _ := strconv.Atoi(f[3])
		fr, err := os.ReadFile(filepath.Join(c2ZDir, f[0]))
		if err != nil {
			t.Fatal(err)
		}
		ents = append(ents, c2ZEntry{name: f[0], n: n, crc: uint32(c), limit: lim})
		frames = append(frames, fr)
	}
	return ents, frames
}

// c2ZParams are the generator's settings: level, window log (0 the
// level's), checksum, content size in the header (0: streamed, so no
// size and no single segment), target compressed block size (0 none),
// literal compression (0 auto, 1 Huffman, 2 raw).
var c2ZParams = []struct {
	name string
	args []string
	wlog int
}{
	{"l3", []string{"3", "0", "1", "1"}, 0},
	{"l19", []string{"19", "0", "0", "1"}, 0},
	{"l1w10", []string{"1", "10", "1", "0"}, 10},
	{"l9w17b", []string{"9", "17", "1", "1", "1340"}, 17},
	{"lm5raw", []string{"-5", "0", "1", "1", "0", "2"}, 0},
	{"l12w20h", []string{"12", "20", "1", "0", "0", "1"}, 20},
}

// TestCompress_ZstdGenCorpus writes std/testdata/zstd: every kind of
// input at lengths around a block (128 KiB) through every setting, the
// first 512 KiB of the real text, and frames back to back with skippable
// frames between them. It runs only with MK_ZGEN naming the generator: a
// C program over libzstd 1.5.7 (the release's sources), args level,
// window log, checksum, content size, target block size, literal mode,
// stdin to stdout.
func TestCompress_ZstdGenCorpus(t *testing.T) {
	gen := os.Getenv("MK_ZGEN")
	if gen == "" {
		t.Skip("MK_ZGEN not set")
	}
	if err := os.MkdirAll(c2ZDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(in []byte, args []string) []byte {
		cmd := exec.Command(gen, args...)
		cmd.Stdin = bytes.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("zgen %v: %v", args, err)
		}
		return out
	}
	var man strings.Builder
	man.WriteString("# name length crc32 window-limit; written by TestCompress_ZstdGenCorpus (libzstd 1.5.7)\n")
	add := func(name string, plain, frame []byte, limit int) {
		if err := os.WriteFile(filepath.Join(c2ZDir, name), frame, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := c2ZstdGo(frame); err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%s: Go's decoder: %d bytes, %v", name, len(got), err)
		}
		fmt.Fprintf(&man, "%s %d %x %d\n", name, len(plain), crc32.ChecksumIEEE(plain), limit)
	}
	for kind := 0; kind < 5; kind++ {
		lengths := []int{0, 1, 300, 5000, 131079, 400000}
		if kind == 0 {
			lengths = []int{0, 1, 300, 5000, 131079}
		}
		for _, n := range lengths {
			plain := czInput(kind, n, uint64(n)+3)
			for _, p := range c2ZParams {
				if kind == 0 && n > 5000 && p.name != "l3" && p.name != "lm5raw" {
					continue // incompressible: two settings are enough
				}
				limit := 0
				if p.wlog != 0 {
					limit = 1 << p.wlog
				}
				add(fmt.Sprintf("k%d-n%d-%s.zst", kind, n, p.name), plain, run(plain, p.args), limit)
			}
		}
	}
	real := czRealInput(t)
	if len(real) > 1<<19 {
		real = real[:1<<19]
	}
	add("real-l19.zst", real, run(real, []string{"19", "0", "1", "1"}), 0)
	// The compress-unzstd bench row's frame: no content checksum, so no
	// cell of the row checks one. The row's bench.json pins checksums of
	// this frame's 512 KiB of text (CRC-32 b8570a67).
	add("real-l19nc.zst", real, run(real, []string{"19", "0", "0", "1"}), 0)
	add("real-l3w15.zst", real, run(real, []string{"3", "15", "1", "0"}), 1<<15)
	a, b := czInput(2, 70000, 1), czInput(4, 9000, 2)
	skip := []byte{0x5A, 0x2A, 0x4D, 0x18, 7, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7}
	empty := []byte{0x50, 0x2A, 0x4D, 0x18, 0, 0, 0, 0}
	multi := bytes.Join([][]byte{run(a, []string{"3", "0", "1", "1"}), skip, run(b, []string{"1", "10", "0", "0"}), empty}, nil)
	add("multi.zst", append(append([]byte{}, a...), b...), multi, 0)
	if err := os.WriteFile(filepath.Join(c2ZDir, "MANIFEST"), []byte(man.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCompress_ZstdCorpus: every stored frame decodes to its length and
// CRC-32 through Read and through the Reader, with input pieces of 1 byte
// to 128 KiB and output pieces of 1 byte to 70,000, under the default
// window limit and, for a frame with a small window, under a limit equal
// to it (so the window wraps every 128 KiB); Go's decoder agrees. Every
// frame of a window over a limit fails with TooLarge under it.
func TestCompress_ZstdCorpus(t *testing.T) {
	ents, frames := c2ZManifest(t)
	exe := c2ZExe(t)
	for _, c := range []struct {
		form       byte
		maxp, maxo int
	}{{0, 1 << 17, 70000}, {1, 4096, 1000}, {0, 7, 65536}, {1, 1 << 17, 1}} {
		if c.maxo == 1 || c.maxp == 7 {
			// One byte at a time costs a call per byte: the small frames only.
			var fs [][]byte
			var es []c2ZEntry
			for i, e := range ents {
				if e.n <= 5000 {
					fs, es = append(fs, frames[i]), append(es, e)
				}
			}
			c2ZCheck(t, exe, es, fs, c.form, c.maxp, c.maxo, 0)
			continue
		}
		c2ZCheck(t, exe, ents, frames, c.form, c.maxp, c.maxo, 0)
	}
	var fs [][]byte
	var es []c2ZEntry
	for i, e := range ents {
		if e.limit != 0 {
			fs, es = append(fs, frames[i]), append(es, e)
		}
	}
	c2ZCheck(t, exe, es, fs, 0, 1<<17, 70000, -1)
	// Under half its window, a frame with a window descriptor is TooLarge;
	// a frame whose window is the default is TooLarge under 1 KiB.
	got := c2ZAsk(t, exe, fs, 0, 5, 1<<17, 70000, 512)
	for i, e := range es {
		if e.n > 512 && got[i].verdict != "toolarge" {
			t.Errorf("%s under a 512-byte window limit: got %v, want toolarge", e.name, got[i])
		}
	}
	for i, e := range ents {
		if g := c2ZstdGoRes(frames[i]); g.verdict != "ok" || g.n != e.n || g.crc != e.crc {
			t.Errorf("%s: Go's decoder %v, want ok %d %x", e.name, g, e.n, e.crc)
		}
	}
	t.Logf("%d frames", len(ents))
}

// c2ZCheck decodes frames and checks each against its entry; wl -1 means
// each entry's own limit.
func c2ZCheck(t *testing.T, exe string, ents []c2ZEntry, frames [][]byte, form byte, maxp, maxo, wl int) {
	t.Helper()
	if wl == -1 {
		for i, e := range ents {
			got := c2ZAsk(t, exe, frames[i:i+1], form, uint64(i), maxp, maxo, e.limit)[0]
			if got.verdict != "ok" || got.n != e.n || got.crc != e.crc {
				t.Errorf("%s (limit %d): got %v, want ok %d %x", e.name, e.limit, got, e.n, e.crc)
			}
		}
		return
	}
	got := c2ZAsk(t, exe, frames, form, 1, maxp, maxo, wl)
	for i, e := range ents {
		if got[i].verdict != "ok" || got[i].n != e.n || got[i].crc != e.crc {
			t.Errorf("%s (form %d, pieces %d/%d): got %v, want ok %d %x", e.name, form, maxp, maxo, got[i], e.n, e.crc)
		}
	}
}

// c2ZEvidence is the zstd project's golden files and Go's zstd test frames
// from Mk-evidence ($MK_EVIDENCE, else next to the repository), by name;
// nil when they are not there.
func c2ZEvidence(t testing.TB) map[string][]byte {
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(root, "compress", "zstd")
	m := map[string][]byte{}
	for _, sub := range []string{"tests/golden-decompression", "tests/golden-decompression-errors", "go-internal-zstd-testdata"} {
		ents, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".zst") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, sub, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			m[sub+"/"+e.Name()] = b
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// TestCompress_ZstdEvidence: the golden decompression files and Go's test
// frames decode as Go's decoder decodes them (Go's frames' names start
// with their output's SHA-256, which Go's own test checks); every golden
// error file fails in both, Truncated where Go's is an unexpected end.
func TestCompress_ZstdEvidence(t *testing.T) {
	ev := c2ZEvidence(t)
	if ev == nil {
		t.Skip("Mk-evidence/compress/zstd not found")
	}
	exe := c2ZExe(t)
	var names []string
	var frames [][]byte
	for n, b := range ev {
		names = append(names, n)
		frames = append(frames, b)
	}
	for form := byte(0); form < 2; form++ {
		got := c2ZAsk(t, exe, frames, form, 3, 1000, 4096, 0)
		for i, n := range names {
			want := c2ZstdGoRes(frames[i])
			if strings.Contains(n, "errors/") && want.verdict == "ok" {
				t.Errorf("%s: Go's decoder accepts a golden error file", n)
			}
			// zeroSeq_2B (a zero sequence count in the 2-byte form) is a
			// valid frame Go refuses: the golden files' verdict wins.
			golden := strings.Contains(n, "golden-decompression/")
			if !c2ZSame(got[i], want) && c2ZstdDiffers(frames[i], got[i], want, 0) == "" && !(golden && want.verdict != "ok") {
				t.Errorf("%s (form %d): got %v, Go %v", n, form, got[i], want)
			}
			if strings.Contains(n, "golden-decompression/") && got[i].verdict != "ok" {
				t.Errorf("%s (form %d): got %v, want the file decoded", n, form, got[i])
			}
		}
	}
	t.Logf("%d files", len(names))
}

// c2ZSame compares a verdict with Go's: equal outputs, or the same failure
// class (Go's corrupt offsets are unknown).
func c2ZSame(got, want czRes) bool {
	if want.verdict == "corrupt" {
		return got.verdict == "corrupt"
	}
	return got.verdict == want.verdict && got.n == want.n && got.crc == want.crc
}

// c2ZRaw is a frame header (magic, descriptor fhd, then the fields) and
// blocks.
func c2ZRaw(parts ...[]byte) []byte {
	return bytes.Join(append([][]byte{{0x28, 0xB5, 0x2F, 0xFD}}, parts...), nil)
}

// c2ZBlockHdr is a block header: last, type, size.
func c2ZBlockHdr(last bool, typ, size int) []byte {
	h := size<<3 | typ<<1
	if last {
		h |= 1
	}
	return []byte{byte(h), byte(h >> 8), byte(h >> 16)}
}

// c2ZEdges are hand-made frames with exact verdicts (offsets included).
func c2ZEdges() []struct {
	name string
	data []byte
	want czRes
} {
	ok := func(b []byte) czRes { return czRes{verdict: "ok", n: len(b), crc: crc32.ChecksumIEEE(b)} }
	hello := []byte("hello, zstd")
	raw := func(fhd byte, fields []byte, last bool) []byte {
		return c2ZRaw([]byte{fhd}, fields, c2ZBlockHdr(last, 0, len(hello)), hello)
	}
	// Single segment (0x20), FCS 1 byte = 11.
	ss := raw(0x20, []byte{11}, true)
	return []struct {
		name string
		data []byte
		want czRes
	}{
		{"raw block single segment", ss, ok(hello)},
		{"raw block window 1 KiB, no size", raw(0x00, []byte{0x00}, true), ok(hello)},
		{"rle block", c2ZRaw([]byte{0x20, 50}, c2ZBlockHdr(true, 1, 50), []byte{'x'}), ok(bytes.Repeat([]byte{'x'}, 50))},
		{"two blocks", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(false, 0, 5), hello[:5], c2ZBlockHdr(true, 1, 3), []byte{'!'}), ok([]byte("hello!!!"))},
		{"empty single segment", c2ZRaw([]byte{0x20, 0}, c2ZBlockHdr(true, 0, 0)), ok(nil)},
		{"skippable only", []byte{0x50, 0x2A, 0x4D, 0x18, 2, 0, 0, 0, 9, 9}, ok(nil)},
		{"skippable then frame", append([]byte{0x5F, 0x2A, 0x4D, 0x18, 0, 0, 0, 0}, ss...), ok(hello)},
		{"two frames", append(append([]byte{}, ss...), ss...), ok(append(append([]byte{}, hello...), hello...))},
		{"empty input", nil, czRes{verdict: "truncated"}},
		{"bad magic", []byte{0x28, 0xB5, 0x2F, 0xFE, 0x20, 0}, czRes{verdict: "corrupt", offset: 0}},
		{"bad magic after a frame", append(append([]byte{}, ss...), 1, 2, 3, 4), czRes{verdict: "corrupt", offset: len(ss)}},
		{"partial magic after a frame", append(append([]byte{}, ss...), 0x28, 0xB5), czRes{verdict: "truncated"}},
		{"reserved bit", raw(0x28, []byte{11}, true), czRes{verdict: "corrupt", offset: 4}},
		{"dictionary id", c2ZRaw([]byte{0x21, 7, 11}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "corrupt", offset: 5}},
		{"dictionary id 0", c2ZRaw([]byte{0x21, 0, 11}, c2ZBlockHdr(true, 0, 11), hello), ok(hello)},
		{"block type 3", c2ZRaw([]byte{0x20, 11}, c2ZBlockHdr(true, 3, 11), hello), czRes{verdict: "corrupt", offset: 6}},
		{"block over the content size", c2ZRaw([]byte{0x20, 10}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "corrupt", offset: 6}},
		{"content size short", c2ZRaw([]byte{0x20, 12}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "corrupt", offset: 6}},
		{"content size over the output", c2ZRaw([]byte{0x40, 0x00, 13, 0}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "corrupt", offset: 8}},
		{"bad checksum", c2ZRaw([]byte{0x24, 11}, c2ZBlockHdr(true, 0, 11), hello, []byte{1, 2, 3, 4}), czRes{verdict: "corrupt", offset: 20}},
		{"truncated checksum", c2ZRaw([]byte{0x24, 11}, c2ZBlockHdr(true, 0, 11), hello, []byte{1, 2}), czRes{verdict: "truncated"}},
		{"truncated block", ss[:len(ss)-1], czRes{verdict: "truncated"}},
		{"truncated header", ss[:5], czRes{verdict: "truncated"}},
		{"window 16 MiB", c2ZRaw([]byte{0x00, 0x70}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "toolarge"}},
		{"single segment 9 MiB", c2ZRaw([]byte{0xA0, 0, 0, 0x90, 0}, c2ZBlockHdr(true, 0, 11), hello), czRes{verdict: "toolarge"}},
		{"truncated skippable", []byte{0x50, 0x2A, 0x4D, 0x18, 9, 0, 0, 0, 1}, czRes{verdict: "truncated"}},
		{"compressed block, empty", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 2, 0)), czRes{verdict: "corrupt", offset: 6}},
		{"compressed block, raw literals, no sequences", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 2, 13), []byte{11 << 3}, hello, []byte{0}), ok(hello)},
		{"compressed block, trailing byte", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 2, 14), []byte{11 << 3}, hello, []byte{0, 0}), czRes{verdict: "corrupt", offset: 6}},
		// The zstd project's golden zeroSeq_2B.zst, byte for byte: no
		// sequences, as a zero count in the 2-byte form (Go refuses it).
		{"compressed block, no sequences in the 2-byte form", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 2, 16), []byte{13 << 3}, []byte("Hello World!\n"), []byte{0x80, 0}), ok([]byte("Hello World!\n"))},
		{"compressed block, no sequences in the 2-byte form, trailing byte", c2ZRaw([]byte{0x00, 0x00}, c2ZBlockHdr(true, 2, 17), []byte{13 << 3}, []byte("Hello World!\n"), []byte{0x80, 0, 0}), czRes{verdict: "corrupt", offset: 6}},
	}
}

// TestCompress_ZstdEdges: the hand-made frames' verdicts, offsets
// included, through Read and the Reader, in pieces of 1 byte and whole;
// Go's decoder agrees but where c2ZstdDiffers says why not.
func TestCompress_ZstdEdges(t *testing.T) {
	exe := c2ZExe(t)
	edges := c2ZEdges()
	var frames [][]byte
	for _, e := range edges {
		frames = append(frames, e.data)
	}
	for _, c := range []struct {
		form       byte
		maxp, maxo int
	}{{0, 1, 1}, {0, 1000, 1000}, {1, 1, 3}, {1, 1000, 1000}} {
		got := c2ZAsk(t, exe, frames, c.form, 1, c.maxp, c.maxo, 0)
		for i, e := range edges {
			if !c2Exact(got[i], e.want) {
				t.Errorf("%s (form %d, pieces %d): got %v, want %v", e.name, c.form, c.maxp, got[i], e.want)
			}
		}
	}
	for _, e := range edges {
		g := c2ZstdGoRes(e.data)
		if !c2ZSame(e.want, g) && c2ZstdDiffers(e.data, e.want, g, 0) == "" {
			t.Errorf("%s: Go's decoder %v, want %v", e.name, g, e.want)
		}
	}
}

// c2ZstdDiffers names the documented difference between this decoder and
// Go's on data, or "" when there is none: Go caps every window at 8 MiB
// where this decoder fails a window over its limit with TooLarge (RFC 8878
// section 3.1.1.1.2 lets a decoder refuse a window over 8 MiB), and Go
// checks a block's size against 128 KiB only, where RFC 8878 section
// 3.1.1.2.3 and the reference decoder also bound it by the window, so a
// single-segment frame's block past its content size is Corrupt here.
func c2ZstdDiffers(data []byte, got, gores czRes, wl int) string {
	if got.verdict == "toolarge" && c2ZOverLimit(data, wl) {
		// Only where a frame header declares a window over wl.
		return "window over the limit"
	}
	if got.verdict == "corrupt" && gores.verdict != "corrupt" && c2ZBlockOverWindow(data) {
		return "block over the window"
	}
	if got.verdict == "corrupt" && gores.verdict == "truncated" && (c2ZFramesWhole(data) || c2ZBlockWhole(data, got.offset)) {
		return "Go reports a read past a block's end as an unexpected end"
	}
	if got.verdict == "ok" && gores.verdict == "truncated" {
		// RFC 8878 section 3.1.1.3.2.1 ends the sequences section only at
		// byte0 == 0, so Go reads a Symbol_Compression_Modes byte after a
		// zero count in the 2-byte form (80 00) and runs off the block;
		// the format's 0.4.0 ("fixed imprecise behavior for nbSeq==0")
		// and libzstd 1.5.7 end it at a zero count in any form, and the
		// zstd project's golden zeroSeq_2B.zst must decode. Go has to
		// agree once each such count is rewritten to the 1-byte form.
		if rw := c2ZZeroSeq1B(data); rw != nil && c2ZSame(got, c2ZstdGoRes(rw)) {
			return "a zero sequence count in the 2-byte form"
		}
	}
	if ref := c2ZRef(data); ref != "" {
		if strings.HasPrefix(ref, c2ZFault) {
			// No verdict, so nothing is documented by it: every caller
			// fails on this input, where a fault
			// read as "err" agreed with any corrupt verdict.
			c2ZFaultOnce.Do(func() { fmt.Fprintf(os.Stderr, "c2ZRef: %s; each input that needs the reference decoder fails\n", ref) })
			return ""
		}
		if (ref == "ok") == (got.verdict == "ok") {
			return "the reference decoder agrees"
		}
		if got.verdict == "corrupt" && gores.verdict == "ok" && ref == "ok" && c2ZHuff4At(data, got.offset) {
			// The format's "Huffman-coded Streams" section calls a
			// stream that is not entirely and exactly consumed faulty;
			// libzstd 1.5.7's fast 4-stream loop and Go check only that
			// each stream gave its symbols (one run: 54 of 363,000
			// corruptions of k2-n5000-l19, every one in its streams).
			return "a 4-stream Huffman literal stream not consumed exactly"
		}
		return ""
	}
	if got.verdict == "corrupt" && gores.verdict == "ok" && c2ZCompressedAt(data, got.offset) {
		// Go does not require a Huffman stream to be read exactly and
		// bounds a block's output by 128 KiB only; RFC 8878 and the
		// reference refuse both. Checked against libzstd when MK_ZDEC is
		// set.
		return "stricter inside a compressed block (MK_ZDEC unset)"
	}
	return ""
}

// c2ZRef is the reference decoder's verdict on data ("ok", "err" or
// "truncated"), when MK_ZDEC names it (libzstd 1.5.7's streaming decoder
// with an 8 MiB window limit, built outside the repository as zgen is);
// "" when it is not set. A zdec that cannot
// start, exits other than 0 (2 is a fault of zdec itself) or answers no
// verdict gives c2ZFault and why, never a verdict.
func c2ZRef(data []byte) string {
	exe := os.Getenv("MK_ZDEC")
	if exe == "" {
		return ""
	}
	cmd := exec.Command(exe)
	cmd.Stdin = bytes.NewReader(data)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("%s MK_ZDEC %s: %v %s", c2ZFault, exe, err, strings.TrimSpace(errb.String()))
	}
	f := strings.Fields(string(out))
	if len(f) == 0 || (f[0] != "ok" && f[0] != "err" && f[0] != "truncated") {
		return fmt.Sprintf("%s MK_ZDEC %s answered %q", c2ZFault, exe, strings.TrimSpace(string(out)))
	}
	return f[0]
}

// c2ZFault starts c2ZRef's answer when the reference decoder gave no
// verdict; c2ZFaultOnce reports the first such answer.
const c2ZFault = "fault:"

var c2ZFaultOnce sync.Once

// TestCompress_ZRefFault: a reference decoder that cannot start, or that
// exits 2 (here the go command run with no arguments), is a fault, not
// the verdict "err", and c2ZstdDiffers then documents nothing, so a
// corrupt verdict of std.compress is no longer "the reference decoder
// agrees". Runs without MK_ZDEC.
func TestCompress_ZRefFault(t *testing.T) {
	frame := []byte{0x28, 0xB5, 0x2F, 0xFD, 0x20, 0x05, 0x29, 0x00, 0x00, 'h', 'e', 'l', 'l', 'o'}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go"+map[bool]string{true: ".exe"}[runtime.GOOS == "windows"])
	for _, exe := range []string{filepath.Join(t.TempDir(), "no-such-zdec.exe"), goExe} {
		t.Setenv("MK_ZDEC", exe)
		if ref := c2ZRef(frame); !strings.HasPrefix(ref, c2ZFault) {
			t.Errorf("MK_ZDEC %s: c2ZRef %q, want a fault", exe, ref)
		}
		if why := c2ZstdDiffers(frame, czRes{verdict: "corrupt"}, czRes{verdict: "ok"}, 0); why != "" {
			t.Errorf("MK_ZDEC %s: c2ZstdDiffers %q, want no documented difference", exe, why)
		}
	}
}

// c2ZHuff4At reports whether data has a compressed block's header at off
// whose literals are Huffman-coded in 4 streams (type 2 or 3, size format
// 1 to 3).
func c2ZHuff4At(data []byte, off int) bool {
	if !c2ZCompressedAt(data, off) || off+3 >= len(data) {
		return false
	}
	h := data[off+3]
	return h&3 >= 2 && (h>>2)&3 != 0
}

// c2ZCompressedAt reports whether data has a compressed block's header at
// off.
func c2ZCompressedAt(data []byte, off int) bool {
	return off >= 0 && off+3 <= len(data) && (data[off]>>1)&3 == 2
}

// c2ZFramesWhole reports whether data holds every byte its frames'
// headers and block headers declare (so it is not truncated), as far as
// they parse.
func c2ZFramesWhole(data []byte) bool {
	return c2ZWalk(data, false)
}

// c2ZBlockWhole reports whether data holds a block header at off whose
// body is all there, so an error inside that block is not a truncation.
func c2ZBlockWhole(data []byte, off int) bool {
	if off < 0 || off+3 > len(data) {
		return false
	}
	h := int(data[off]) | int(data[off+1])<<8 | int(data[off+2])<<16
	n := h >> 3
	if (h>>1)&3 == 1 {
		n = 1
	}
	return off+3+n <= len(data)
}

// c2ZBlockOverWindow walks data's frames as far as their headers parse and
// reports whether a block's size field is over its frame's window (a
// single-segment frame's content size) while not over 128 KiB.
func c2ZBlockOverWindow(data []byte) bool {
	return c2ZWalk(data, true)
}

// c2ZWalk walks data's frames: with over, it reports whether a block's
// size is over its window and not over 128 KiB; without, whether no frame
// is cut short.
func c2ZWalk(data []byte, over bool) bool {
	le := binary.LittleEndian
	i := 0
	for i+4 <= len(data) {
		magic := le.Uint32(data[i:])
		if magic&0xFFFFFFF0 == 0x184D2A50 {
			if i+8 > len(data) {
				return false
			}
			i += 8 + int(le.Uint32(data[i+4:]))
			continue
		}
		if magic != 0xFD2FB528 {
			return !over
		}
		if i+5 > len(data) {
			return false
		}
		fhd := data[i+4]
		i += 5
		ss := fhd&0x20 != 0
		fcsz := []int{0, 2, 4, 8}[fhd>>6]
		if fhd>>6 == 0 && ss {
			fcsz = 1
		}
		didz := []int{0, 1, 2, 4}[fhd&3]
		var window uint64
		if !ss {
			if i >= len(data) {
				return false
			}
			wd := data[i]
			base := uint64(1) << (10 + wd>>3)
			window = base + base/8*uint64(wd&7)
			i++
		}
		i += didz
		if i+fcsz > len(data) {
			return false
		}
		var fcs uint64
		switch fcsz {
		case 1:
			fcs = uint64(data[i])
		case 2:
			fcs = 256 + uint64(le.Uint16(data[i:]))
		case 4:
			fcs = uint64(le.Uint32(data[i:]))
		case 8:
			fcs = le.Uint64(data[i:])
		}
		i += fcsz
		if ss {
			window = fcs
		}
		for {
			if i+3 > len(data) {
				return false
			}
			h := int(data[i]) | int(data[i+1])<<8 | int(data[i+2])<<16
			size := h >> 3
			if over && uint64(size) > window && size <= 128<<10 {
				return true
			}
			if (h>>1)&3 == 3 {
				return !over
			}
			n := size
			if (h>>1)&3 == 1 {
				n = 1
			}
			i += 3 + n
			if h&1 != 0 {
				if fhd&4 != 0 {
					i += 4
				}
				break
			}
		}
	}
	return !over && i == len(data)
}

// TestCompress_ZstdTruncations: every prefix of small frames of every
// setting is Truncated (or, cut right after a whole frame, that frame's
// output), through Read and the Reader; Go agrees.
func TestCompress_ZstdTruncations(t *testing.T) {
	ents, frames := c2ZManifest(t)
	exe := c2ZExe(t)
	var cuts [][]byte
	var wants []czRes
	for i, e := range ents {
		if e.n != 300 && e.n != 1 {
			continue
		}
		f := frames[i]
		for k := 0; k < len(f); k++ {
			cuts = append(cuts, f[:k])
			wants = append(wants, czRes{verdict: "truncated"})
		}
	}
	for form := byte(0); form < 2; form++ {
		got := c2ZAsk(t, exe, cuts, form, 9, 3, 100, 0)
		for i := range cuts {
			if !c2Exact(got[i], wants[i]) {
				t.Errorf("prefix of %d bytes (form %d): got %v, want truncated", len(cuts[i]), form, got[i])
			}
		}
	}
	for i := range cuts {
		if g := c2ZstdGoRes(cuts[i]); g.verdict != "truncated" {
			t.Errorf("prefix of %d bytes: Go says %v", len(cuts[i]), g)
		}
	}
	t.Logf("%d prefixes", len(cuts))
}

// TestCompress_ZstdCorruptVerdicts: seeded corruptions of stored frames
// (bit flips, byte changes, cuts and duplications) through Read: no crash
// or hang, and the verdict is Go's (equal output, or the same failure
// class) but for c2ZstdDiffers' cases, which are counted; Read and the
// Reader agree.
func TestCompress_ZstdCorruptVerdicts(t *testing.T) {
	ents, frames := c2ZManifest(t)
	exe := c2ZExe(t)
	var valid [][]byte
	for i, e := range ents {
		if e.n <= 5000 || strings.HasPrefix(e.name, "multi") {
			valid = append(valid, frames[i])
		}
	}
	// MK_ZMUT sets the mutations per frame (60 by default); an unexplained
	// batch failure was once seen at 3,000.
	per := 60
	if v, err := strconv.Atoi(os.Getenv("MK_ZMUT")); err == nil && v > 0 {
		per = v
	}
	bad := c2Mutate(valid, per, 77)
	got := c2ZAskOrFind(t, exe, bad, 0, 0)
	gotr := c2ZAskOrFind(t, exe, bad, 1, 0)
	differs := map[string]int{}
	for i, b := range bad {
		g := c2ZstdGoRes(b)
		if !c2ZSame(got[i], g) {
			why := c2ZstdDiffers(b, got[i], g, 0)
			if why == "" {
				t.Errorf("input %d (% x): got %v, Go %v", i, b[:min(len(b), 48)], got[i], g)
			}
			differs[why]++
		}
		if !c2Exact(gotr[i], got[i]) && gotr[i].verdict != got[i].verdict {
			t.Errorf("input %d: Reader %v, Read %v", i, gotr[i], got[i])
		}
	}
	t.Logf("%d corruptions; documented differences from Go: %v", len(bad), differs)
	// Under a limit equal to the frame's window the window wraps every
	// ~128 KiB: corruptions of the long small-window frames there, where an
	// offset past the window must still read the right bytes or fail.
	for _, w := range []struct {
		prefix string
		limit  int
	}{{"-n400000-l1w10", 1024}, {"real-l3w15", 1 << 15}} {
		var long [][]byte
		for i, e := range ents {
			if strings.Contains(e.name, w.prefix) {
				long = append(long, frames[i])
			}
		}
		bad := c2Mutate(long, 40, 91)
		got := c2ZAskOrFind(t, exe, bad, 0, w.limit)
		for i, b := range bad {
			g := c2ZstdGoRes(b)
			if !c2ZSame(got[i], g) && c2ZstdDiffers(b, got[i], g, w.limit) == "" {
				t.Errorf("%s limit %d input %d: got %v, Go %v", w.prefix, w.limit, i, got[i], g)
			}
		}
		t.Logf("%s under limit %d: %d corruptions", w.prefix, w.limit, len(bad))
	}
}

// c2ZAskOrFind is c2ZAsk (input pieces up to 1000 bytes, output 4096,
// the default limit) that, when the batch program fails, runs each input
// alone and fails the test with the first input the program fails on.
func c2ZAskOrFind(t *testing.T, exe string, frames [][]byte, form byte, wl int) []czRes {
	t.Helper()
	run := func(fs [][]byte, seed uint64) ([]byte, error) {
		recs := make([]czRec, len(fs))
		for i, f := range fs {
			recs[i] = czRec{op: form, limit: int64(seed + uint64(i)*7919), data: f}
		}
		cmd := exec.Command(exe, "1000", "4096", fmt.Sprint(wl))
		cmd.Stdin = bytes.NewReader(czEncode(recs))
		var errb bytes.Buffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("%v: %s", err, errb.String())
		}
		return out, nil
	}
	out, err := run(frames, 4)
	if err == nil {
		br := bufio.NewReader(bytes.NewReader(out))
		res := make([]czRes, 0, len(frames))
		for range frames {
			r, perr := czParse(br)
			if perr != nil {
				err = perr
				break
			}
			res = append(res, r)
		}
		if err == nil {
			return res
		}
	}
	for i, f := range frames {
		if _, e := run([][]byte{f}, 4+uint64(i)*7919); e != nil {
			t.Fatalf("form %d: the program fails on input %d (% x): %v", form, i, f, e)
		}
	}
	t.Fatalf("form %d: the batch failed (%v) but no input fails alone", form, err)
	return nil
}

var c2ZSrv c2Pool

// c2ZServe sends recs to a running batch program (one per test binary;
// default window limit, pieces up to 1000 and 4096 bytes).
func c2ZServe(t *testing.T, recs []czRec) []czRes {
	return c2ZSrv.ask(t, "zstd program", func() *exec.Cmd {
		return exec.Command(c2ZExe(t), "1000", "4096", "0")
	}, recs)
}

// c2ZFuzzRecs are a fuzz input's records: Read and the Reader of New, and
// Read and ReaderLazy of NewLazy.
func c2ZFuzzRecs(data []byte) []czRec {
	return []czRec{{op: 0, limit: 1, data: data}, {op: 1, limit: 2, data: data}, {op: 3, limit: 3, data: data}, {op: 4, limit: 4, data: data}}
}

// c2ZFuzzSame fails t unless every record's verdict, and its output when
// ok, is Read's: a NewLazy decoder, whose windows are sized to the frames,
// decodes exactly as New's, which holds the limit's window.
func c2ZFuzzSame(t *testing.T, data []byte, got []czRes) {
	t.Helper()
	for i, name := range []string{"Read", "Reader", "lazy Read", "ReaderLazy"} {
		if got[i].verdict != got[0].verdict || (got[0].verdict == "ok" && !c2Exact(got[i], got[0])) {
			t.Errorf("(% x): Read %v, %s %v", data[:min(len(data), 64)], got[0], name, got[i])
		}
	}
}

// FuzzCompress_Zstd: any input, through Read and through the
// Reader of one long-running dev build (every check on), and through Read
// and ReaderLazy of a NewLazy decoder: no panic, no hang, each
// verdict equal to Read's, and Read's verdict Go's but for c2ZstdDiffers'
// cases. Seeded with the stored frames of up to 5,000 bytes, the evidence
// files and the hand-made edges.
func FuzzCompress_Zstd(f *testing.F) {
	if ents, frames := c2ZManifest(f); len(ents) > 0 {
		for i, e := range ents {
			if e.n <= 5000 {
				f.Add(frames[i])
			}
		}
	}
	for _, b := range c2ZEvidence(f) {
		if len(b) < 10000 {
			f.Add(b)
		}
	}
	for _, e := range c2ZEdges() {
		f.Add(e.data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		got := c2ZServe(t, c2ZFuzzRecs(data))
		c2ZFuzzSame(t, data, got)
		g := c2ZstdGoRes(data)
		if !c2ZSame(got[0], g) && c2ZstdDiffers(data, got[0], g, 0) == "" {
			t.Errorf("(% x): got %v, Go %v", data[:min(len(data), 64)], got[0], g)
		}
	})
}
