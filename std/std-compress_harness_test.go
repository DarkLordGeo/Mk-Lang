// Why: thousands of std.compress cases need one compiled oracle and models to compare it with.
// What: one Mk oracle program that answers stdin records (an operation, a limit, the input) with verdict lines,
// Go models of each operation over compress/flate and compress/gzip, and the shared generated inputs.
// How: the oracle is built once per test binary over czStdCopy's std (MK_COMPRESS_BASE names a snapshot).

package std

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
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
	"time"
)

// The oracle's operations.
const (
	czInflate = 1
	czGunzip  = 2
	czDeflate = 3
	czGzip    = 4
)

// czNoLimit is the limit a record passes when it tests no limit.
const czNoLimit = 1 << 40

// czOracle reads records of op (1 byte), limit (8 bytes, little-endian),
// n (4 bytes) and n bytes of input until stdin ends, and prints for each:
// "ok <len> <crc32 hex>" for a decoded output, "enc <len>" and the raw
// encoded bytes for an encoding, or the error ("corrupt <offset>",
// "truncated", "toolarge", "oom"). It flushes after every record, so a Go
// fuzz target can keep one running and send it one record at a time.
const czOracle = `import std.compress
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

Decoded(out: mut Writer, r: []u8): WriteError!void {
    n := r.Len()
    c := hash.Crc32(r)
    try Print(out, "ok {n} {c:x}\n", (n: n, c: c))
}

Encoded(out: mut Writer, r: []u8): WriteError!void {
    n := r.Len()
    try Print(out, "enc {n}\n", (n: n))
    try out.Bytes(r)
}

Do(out: mut Writer, a: &Arena, op: u8, data: []u8, limit: int): WriteError!void {
    if op == 1 {
        r := compress.Inflate(a, data, limit) else e {
            try Verdict(out, e)
            return
        }
        try Decoded(out, r)
        return
    }
    if op == 2 {
        r := compress.Gunzip(a, data, limit) else e {
            try Verdict(out, e)
            return
        }
        try Decoded(out, r)
        return
    }
    if op == 3 {
        r := compress.Deflate(a, data) else {
            try Print(out, "oom\n")
            return
        }
        try Encoded(out, r)
        return
    }
    if op == 4 {
        r := compress.Gzip(a, data) else {
            try Print(out, "oom\n")
            return
        }
        try Encoded(out, r)
        return
    }
    try Print(out, "badop\n")
}

export Main(sys: &System): WriteError|ReadError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
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
        try Do(mut out, &a, hdr[0], data[..], Word(hdr[..], 1, 8))
        try out.Flush()
        a.Reset()
    }
}
`

// czStdCopy writes the std the tests build against under root. With
// MK_COMPRESS_BASE set (a snapshot of std, for when std's files are
// mid-edit) it is that snapshot with std/compress
// replaced by this tree's; else the live std.
func czStdCopy(t testing.TB, root string) string {
	t.Helper()
	base := os.Getenv("MK_COMPRESS_BASE")
	if base == "" {
		tt, ok := t.(*testing.T)
		if !ok {
			t.Fatal("czStdCopy: a live std copy needs a *testing.T")
		}
		return copyStd(tt, root, nil)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "compress")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "compress"), os.DirFS("compress")); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// czBuild builds src as main.mk with the given flags and returns the
// executable. A missing LLVM skips the test.
func czBuild(t *testing.T, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := czStdCopy(t, root)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	var out string
	var status int
	// The tree's checker has a rare order-dependent ICE ("facts of
	// std.rt.Site asked before its module's impls are registered");
	// a retry is a fresh order.
	for try := 0; try < 3; try++ {
		out, status = mkCmd(t, root, stdDir, args...)
		if status != 3 || !strings.Contains(out, "asked before its module's impls are registered") {
			break
		}
	}
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

var czExes struct {
	sync.Mutex
	m map[string]string
}

// czOracleExe builds the oracle once per test binary and mode ("" for a
// dev build, "--release").
func czOracleExe(t *testing.T, mode string) string {
	t.Helper()
	czExes.Lock()
	defer czExes.Unlock()
	if exe, ok := czExes.m[mode]; ok {
		return exe
	}
	var flags []string
	if mode != "" {
		flags = append(flags, mode)
	}
	// Built outside t.TempDir, which ends with the test that made it;
	// TestMain removes it.
	dir, err := packageTempDir("mkcompress")
	if err != nil {
		t.Fatal(err)
	}
	exe := czBuildIn(t, dir, czOracle, flags...)
	if czExes.m == nil {
		czExes.m = map[string]string{}
	}
	czExes.m[mode] = exe
	return exe
}

// czBuildIn is czBuild into dir.
func czBuildIn(t *testing.T, dir, src string, flags ...string) string {
	t.Helper()
	return czBuildStd(t, dir, czStdCopy(t, dir), src, flags...)
}

// czBuildStd is czBuildIn over the std copy at stdDir.
func czBuildStd(t *testing.T, dir, stdDir, src string, flags ...string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	var out string
	var status int
	for try := 0; try < 3; try++ {
		out, status = mkCmd(t, dir, stdDir, args...)
		if status != 3 || !strings.Contains(out, "asked before its module's impls are registered") {
			break
		}
	}
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// czRec is one oracle record.
type czRec struct {
	op    byte
	limit int64
	data  []byte
	name  string // for messages
}

// czRes is the oracle's answer to one record.
type czRes struct {
	verdict string // "ok", "corrupt", "truncated", "toolarge", "oom", "enc"
	n       int
	crc     uint32
	offset  int
	enc     []byte
}

func (r czRes) String() string {
	switch r.verdict {
	case "ok":
		return fmt.Sprintf("ok %d %x", r.n, r.crc)
	case "corrupt":
		return fmt.Sprintf("corrupt %d", r.offset)
	case "enc":
		return fmt.Sprintf("enc %d", len(r.enc))
	}
	return r.verdict
}

func czEncode(recs []czRec) []byte {
	var b bytes.Buffer
	for _, r := range recs {
		var h [13]byte
		h[0] = r.op
		binary.LittleEndian.PutUint64(h[1:], uint64(r.limit))
		binary.LittleEndian.PutUint32(h[9:], uint32(len(r.data)))
		b.Write(h[:])
		b.Write(r.data)
	}
	return b.Bytes()
}

// czParse reads one answer from br.
func czParse(br *bufio.Reader) (czRes, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return czRes{}, fmt.Errorf("oracle output ended: %q, %v", line, err)
	}
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) == 0 {
		return czRes{}, fmt.Errorf("empty oracle line")
	}
	r := czRes{verdict: f[0]}
	switch f[0] {
	case "ok":
		if len(f) != 3 {
			return r, fmt.Errorf("bad line %q", line)
		}
		r.n, _ = strconv.Atoi(f[1])
		c, _ := strconv.ParseUint(f[2], 16, 32)
		r.crc = uint32(c)
	case "corrupt":
		r.offset, _ = strconv.Atoi(f[1])
	case "enc":
		n, _ := strconv.Atoi(f[1])
		r.enc = make([]byte, n)
		if _, err := io.ReadFull(br, r.enc); err != nil {
			return r, fmt.Errorf("short encoding: %v", err)
		}
	case "truncated", "toolarge", "oom":
	default:
		return r, fmt.Errorf("bad line %q", line)
	}
	return r, nil
}

// czRun runs exe over recs and returns its answers.
func czRun(t *testing.T, exe string, recs []czRec) []czRes {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Stdin = bytes.NewReader(czEncode(recs))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("oracle killed after 5 minutes")
	}
	br := bufio.NewReader(&out)
	res := make([]czRes, 0, len(recs))
	for range recs {
		r, perr := czParse(br)
		if perr != nil {
			last := "none"
			if len(res) < len(recs) {
				last = recs[len(res)].name
			}
			t.Fatalf("after %d answers (next record %s): %v\nexit: %v\nstderr:\n%s", len(res), last, perr, err, errb.String())
		}
		res = append(res, r)
	}
	if err != nil {
		t.Fatalf("oracle: %v\nstderr:\n%s", err, errb.String())
	}
	return res
}

// czModel is the Go model's answer to a decode record: compress/flate or
// compress/gzip over a bytes.Reader (a ByteReader, so flate reads no
// byte past the stream), read to the end. A raw stream that leaves bytes
// unread is corrupt (Inflate takes the whole input as the stream).
func czModel(r czRec) czRes {
	var out []byte
	var err error
	switch r.op {
	case czInflate:
		br := bytes.NewReader(r.data)
		fr := flate.NewReader(br)
		out, err = io.ReadAll(fr)
		if err == nil && br.Len() != 0 {
			return czRes{verdict: "corrupt", offset: len(r.data) - br.Len()}
		}
	case czGunzip:
		return czGunzipModel(r)
	default:
		panic("czModel: not a decode op")
	}
	if err != nil {
		var ce flate.CorruptInputError
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
			return czRes{verdict: "truncated"}
		case errors.As(err, &ce):
			return czRes{verdict: "corrupt", offset: int(ce)}
		case errors.Is(err, gzip.ErrHeader), errors.Is(err, gzip.ErrChecksum):
			return czRes{verdict: "corrupt", offset: -1}
		}
		return czRes{verdict: "error: " + err.Error()}
	}
	if int64(len(out)) > max(r.limit, 0) {
		return czRes{verdict: "toolarge"}
	}
	return czRes{verdict: "ok", n: len(out), crc: crc32.ChecksumIEEE(out)}
}

// czSame reports whether the oracle's answer matches the model's, offsets
// aside (Go's CorruptInputError is its reader's position, not the first
// bad bit's byte).
func czSame(got, want czRes) bool {
	if got.verdict != want.verdict {
		return false
	}
	if got.verdict == "ok" {
		return got.n == want.n && got.crc == want.crc
	}
	return true
}

// czRng is splitmix64.
type czRng struct{ s uint64 }

func (r *czRng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func (r *czRng) intn(n int) int { return int(r.next() % uint64(n)) }

// czWords is a vocabulary for text-like inputs.
var czWords = strings.Fields(`the of and to in a is that for it as was with be by on not he this are or his
from at which but have an they you were her she there been one all we their has would when if so no will
deflate inflate huffman window match literal distance length block stored fixed dynamic code tree
compress stream buffer arena slice error corrupt offset limit gzip member header trailer checksum`)

// czInput makes n bytes of one of five kinds: 0 random, 1 zeros, 2 text
// from czWords, 3 runs of random lengths, 4 a short repeated pattern with
// sparse noise (long matches at distances 1 to 64).
func czInput(kind, n int, seed uint64) []byte {
	r := czRng{seed*0x100 + uint64(kind)}
	b := make([]byte, 0, n)
	switch kind {
	case 0:
		for len(b) < n {
			b = append(b, byte(r.next()))
		}
	case 1:
		b = b[:n]
	case 2:
		for len(b) < n {
			w := czWords[r.intn(len(czWords))]
			b = append(b, w...)
			if r.intn(12) == 0 {
				b = append(b, ".\n"...)
			} else {
				b = append(b, ' ')
			}
		}
	case 3:
		for len(b) < n {
			c := byte(r.next())
			k := 1 + r.intn(300)
			for i := 0; i < k; i++ {
				b = append(b, c)
			}
		}
	case 4:
		p := make([]byte, 1+r.intn(64))
		for i := range p {
			p[i] = byte(r.next())
		}
		for len(b) < n {
			if r.intn(97) == 0 {
				b = append(b, byte(r.next()))
			} else {
				b = append(b, p[len(b)%len(p)])
			}
		}
	}
	return b[:n]
}

// czLengths are the input lengths of the generated set: small ones, and
// the edges of a stored block (65535), the window (32768) and Deflate's
// block of symbols.
var czLengths = []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 100, 255, 256, 257, 258, 259, 1000, 4095, 4096,
	16383, 16384, 16385, 32767, 32768, 32769, 65535, 65536, 65537, 100000, 300000, 1 << 20}

// czLevels are the Go compressor levels of the generated set.
var czLevels = []int{flate.NoCompression, flate.BestSpeed, 2, 4, flate.DefaultCompression, flate.BestCompression, flate.HuffmanOnly}

func czDeflateGo(t testing.TB, b []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, level)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func czGzipGo(t testing.TB, b []byte, level int) []byte {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// czRealPath holds czRealInput's text, gzip-compressed: Isaac Newton's
// Opticks (fourth edition, 1730), the public-domain Project Gutenberg
// e-text that Go ships as src/testdata/Isaac.Newton-Opticks.txt, 567,198
// bytes (CRC-32 de1864c0).
const czRealPath = "testdata/opticks.txt.gz"

var czReal struct {
	once sync.Once
	text []byte
	err  error
}

// czRealInput is a fixed English prose text for the compressors and
// checksums, decompressed once; each call returns its own copy.
func czRealInput(t testing.TB) []byte {
	czReal.once.Do(func() {
		f, err := os.Open(czRealPath)
		if err != nil {
			czReal.err = err
			return
		}
		defer f.Close()
		r, err := gzip.NewReader(f)
		if err != nil {
			czReal.err = err
			return
		}
		czReal.text, czReal.err = io.ReadAll(r)
		if czReal.err == nil && (len(czReal.text) != 567198 || crc32.ChecksumIEEE(czReal.text) != 0xde1864c0) {
			czReal.err = fmt.Errorf("%s: %d bytes, CRC-32 %08x; want 567198 bytes, de1864c0", czRealPath, len(czReal.text), crc32.ChecksumIEEE(czReal.text))
		}
	})
	if czReal.err != nil {
		t.Fatal(czReal.err)
	}
	return bytes.Clone(czReal.text)
}

// czGunzipModel is the Go model of Gunzip: members parsed by RFC 1952
// section 2.3 (an error at the first byte
// that cannot continue a header, reserved FLG bits refused, FHCRC
// checked), each deflate stream read by compress/flate over a
// bytes.Reader, and the CRC-32 and ISIZE checked. It differs from Go's
// gzip.Reader in two documented ways: gzip.Reader reads 10 header bytes
// before it looks at any (so a short bad header is io.ErrUnexpectedEOF),
// and it ignores reserved FLG bits, which RFC 1952 says a decompressor
// must refuse.
func czGunzipModel(r czRec) czRes {
	d := r.data
	n := len(d)
	var out []byte
	p := 0
	corrupt := func(off int) czRes { return czRes{verdict: "corrupt", offset: off} }
	trunc := czRes{verdict: "truncated"}
	for {
		for i, want := range []byte{0x1f, 0x8b, 8} {
			if p+i >= n {
				return trunc
			}
			if d[p+i] != want {
				return corrupt(p + i)
			}
		}
		if p+3 >= n {
			return trunc
		}
		flg := d[p+3]
		if flg&0xe0 != 0 {
			return corrupt(p + 3)
		}
		if n-p < 10 {
			return trunc
		}
		q := p + 10
		if flg&4 != 0 {
			if n-q < 2 {
				return trunc
			}
			x := int(d[q]) | int(d[q+1])<<8
			q += 2
			if n-q < x {
				return trunc
			}
			q += x
		}
		for _, bit := range []byte{8, 16} {
			if flg&bit != 0 {
				k := bytes.IndexByte(d[q:], 0)
				if k < 0 {
					return trunc
				}
				q += k + 1
			}
		}
		if flg&2 != 0 {
			if n-q < 2 {
				return trunc
			}
			if uint16(crc32.ChecksumIEEE(d[p:q])) != uint16(d[q])|uint16(d[q+1])<<8 {
				return corrupt(q)
			}
			q += 2
		}
		br := bytes.NewReader(d[q:])
		m, err := io.ReadAll(flate.NewReader(br))
		if err != nil {
			var ce flate.CorruptInputError
			if errors.As(err, &ce) {
				return corrupt(q + int(ce))
			}
			return trunc
		}
		end := n - br.Len()
		if n-end < 8 {
			return trunc
		}
		if binary.LittleEndian.Uint32(d[end:]) != crc32.ChecksumIEEE(m) {
			return corrupt(end)
		}
		if binary.LittleEndian.Uint32(d[end+4:]) != uint32(len(m)) {
			return corrupt(end + 4)
		}
		out = append(out, m...)
		p = end + 8
		if p == n {
			break
		}
	}
	if int64(len(out)) > max(r.limit, 0) {
		return czRes{verdict: "toolarge"}
	}
	return czRes{verdict: "ok", n: len(out), crc: crc32.ChecksumIEEE(out)}
}
