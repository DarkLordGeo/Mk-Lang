// Why: the zlib and streaming tests need compiled Mk programs and an independent model to compare with.
// What: three Mk programs built once per test binary (the one-shot zlib oracle, the streaming decoder over
// stdin, the streaming Encoder to stdout) and a Go model of Unzlib.
// How: the model checks RFC 1950's header rules, decodes the body with compress/flate and checks the Adler-32 trailer.

package std

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"hash/adler32"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The oracle's operations (czOracle's record format; op 1 to 4 are its).
const (
	c2Unzlib  = 11
	c2Zlib    = 12
	c2Adler32 = 13
)

// c2Oracle answers records: "ok <len> <crc32 hex>" for Unzlib's output or
// its error line, "enc <len>" and the bytes for Zlib, "adler <hex>" for
// Adler32.
const c2Oracle = `import std.compress
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

Do(out: mut Writer, a: &Arena, op: u8, data: []u8, limit: int): WriteError!void {
    if op == 11 {
        r := compress.Unzlib(a, data, limit) else e {
            try Verdict(out, e)
            return
        }
        n := r.Len()
        c := hash.Crc32(r)
        try Print(out, "ok {n} {c:x}\n", (n: n, c: c))
        return
    }
    if op == 12 {
        r := compress.Zlib(a, data) else {
            try Print(out, "oom\n")
            return
        }
        n := r.Len()
        try Print(out, "enc {n}\n", (n: n))
        try out.Bytes(r)
        return
    }
    if op == 13 {
        x := compress.Adler32(data)
        try Print(out, "adler {x:x}\n", (x: x))
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

// c2StreamProg decodes stdin with an Inflater: args mode (0 raw, 1 gzip, 2
// zlib), form (0 Read, 1 Reader), seed, largest output piece. Prints "ok
// <len> <crc32 hex>", or Read's error line, or for the Reader "readerror"
// and Cause's line ("cause none" when Cause is none), then whether a
// second read fails again ("again").
const c2StreamProg = `import std.compress
import std.hash

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

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    mode := try sys.args[1].ParseInt()
    form := try sys.args[2].ParseInt()
    var s := Cast[u64](try sys.args[3].ParseInt())
    maxc := Cast[int](try sys.args[4].ParseInt())
    buf := try sys.arena.Slice[u8](maxc)
    var z := compress.Inflater.New(sys.arena)
    if mode == 1 {
        z = compress.Inflater.NewGzip(sys.arena)
    } else if mode == 2 {
        z = compress.Inflater.NewZlib(sys.arena)
    }
    var h := hash.Crc32State.New()
    var total := 0
    if form == 1 {
        var rb: [700]u8 = [0; 700]
        var r := z.Reader(mut inp, mut rb[..])
        for {
            s = s +% 0x9E3779B97F4A7C15
            k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxc))
            got := r.Read(mut buf[0..k]) else {
                try Print(mut out, "readerror\n")
                again := r.Read(mut buf[0..k]) else {
                    0
                }
                try Print(mut out, "again {again}\n", (again: again))
                break
            }
            if got == 0 {
                c := h.Sum()
                try Print(mut out, "ok {total} {c:x}\n", (total: total, c: c))
                break
            }
            h.Bytes(buf[0..got])
            total += got
        }
        if e := z.Cause() {
            try Print(mut out, "cause ")
            try Verdict(mut out, e)
        } else {
            try Print(mut out, "cause none\n")
        }
        try out.Flush()
        return
    }
    for {
        s = s +% 0x9E3779B97F4A7C15
        k := 1 + Trunc[int]((s >> 33) % Cast[u64](maxc))
        got := z.Read(mut inp, mut buf[0..k]) else e {
            match e {
                compress.Corrupt(offset: o) {
                    try Print(mut out, "corrupt {o}\n", (o: o))
                }
                compress.Truncated {
                    try Print(mut out, "truncated\n")
                }
                compress.TooLarge {
                    try Print(mut out, "toolarge\n")
                }
                OutOfMemory {
                    try Print(mut out, "oom\n")
                }
                ReadError {
                    try Print(mut out, "readerror\n")
                }
                _ {
                    Panic("compress.Error gained a member this match does not handle")
                }
            }
            try out.Flush()
            return
        }
        if got == 0 {
            break
        }
        h.Bytes(buf[0..got])
        total += got
    }
    c := h.Sum()
    try Print(mut out, "ok {total} {c:x}\n", (total: total, c: c))
    try out.Flush()
}
`

// c2EncProg compresses one record from stdin (czOracle's 13-byte header,
// then the data) with an Encoder to stdout: args framing (0 raw, 1 gzip, 2
// zlib), level, seed, largest input piece, flush chance (a Flush after a
// piece with chance 1 in this, 0 never), and stop (Abandon after this many
// flushes, 0 never). Writes each flush's input position to stderr as
// "flush <pos>".
const c2EncProg = `import std.compress

Word(b: []u8, i: int, k: int): int {
    var x := 0
    for j in 0..k {
        x = x | (int(b[i + j]) << (8 * j))
    }
    return x
}

export Main(sys: &System): WriteError|ReadError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    var errw := sys.Stderr()
    var inp := sys.Stdin()
    f := try sys.args[1].ParseInt()
    level := Cast[int](try sys.args[2].ParseInt())
    var s := Cast[u64](try sys.args[3].ParseInt())
    maxp := Cast[int](try sys.args[4].ParseInt())
    chance := Cast[u64](try sys.args[5].ParseInt())
    stop := Cast[int](try sys.args[6].ParseInt())
    var hdr: [13]u8 = [0; 13]
    _ = try inp.ReadAll(mut hdr[..])
    n := Word(hdr[..], 9, 4)
    data := try sys.arena.Slice[u8](n)
    _ = try inp.ReadAll(data)
    framing := if f == 1 { compress.Framing.Gzip } else if f == 2 { compress.Framing.Zlib } else { compress.Framing.Raw }
    var e := try compress.Encoder.New(sys.arena, framing, level)
    onerr e.Abandon()
    var p := 0
    var flushes := 0
    for p < n {
        s = s +% 0x9E3779B97F4A7C15
        k0 := 1 + Trunc[int]((s >> 33) % Cast[u64](maxp))
        k := if k0 > n - p { n - p } else { k0 }
        try e.Write(mut out, data[p..p + k])
        p += k
        if chance > 0 && (s >> 7) % chance == 0 {
            try e.Flush(mut out)
            flushes += 1
            try Print(mut errw, "flush {p}\n", (p: p))
            if flushes == stop {
                e.Abandon()
                try errw.Flush()
                return
            }
        }
    }
    try e.Finish(mut out)
    try out.Flush()
    try errw.Flush()
}
`

var c2Exes struct {
	sync.Mutex
	m map[string]string
}

// c2Exe builds src once per test binary (a dev build, every check on;
// "--release" in flags for a release build).
func c2Exe(t *testing.T, name, src string, flags ...string) string {
	t.Helper()
	key := name + strings.Join(flags, " ")
	c2Exes.Lock()
	defer c2Exes.Unlock()
	if exe, ok := c2Exes.m[key]; ok {
		return exe
	}
	dir, err := packageTempDir("mkcompress2")
	if err != nil {
		t.Fatal(err)
	}
	exe := czBuildIn(t, dir, src, flags...)
	if c2Exes.m == nil {
		c2Exes.m = map[string]string{}
	}
	c2Exes.m[key] = exe
	return exe
}

// c2PieceExe builds src once per test binary and flags over a std copy
// with czPieceShim (the PieceReader) and the extra shims in std/compress,
// and std.compress in the copy's unsafe_modules for the shims' unsafe
// blocks (the live std.compress has none).
func c2PieceExe(t testing.TB, name, src string, shims map[string]string, flags ...string) string {
	t.Helper()
	tt, ok := t.(*testing.T)
	if !ok {
		t.Fatal("c2PieceExe needs a *testing.T")
	}
	key := "piece:" + name + strings.Join(flags, " ")
	c2Exes.Lock()
	defer c2Exes.Unlock()
	if exe, ok := c2Exes.m[key]; ok {
		return exe
	}
	dir, err := packageTempDir("mkcompress2" + name)
	if err != nil {
		t.Fatal(err)
	}
	stdDir := czStdCopy(t, dir)
	all := map[string]string{"testpieces.mk": czPieceShim}
	for k, v := range shims {
		all[k] = v
	}
	for file, s := range all {
		if err := os.WriteFile(filepath.Join(stdDir, "compress", file), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// MK_C2_PATCH ("file|from|to", by hand only) mutates std/compress/file
	// of the copy, so a fuzz target can be shown to find a planted fault.
	if p := os.Getenv("MK_C2_PATCH"); p != "" {
		f := strings.SplitN(p, "|", 3)
		if len(f) != 3 {
			t.Fatalf("MK_C2_PATCH %q: want file|from|to", p)
		}
		src := filepath.Join(stdDir, "compress", f[0])
		b, err := os.ReadFile(src)
		if err != nil || !bytes.Contains(b, []byte(f[1])) {
			t.Fatalf("MK_C2_PATCH: %s has no %q (%v)", f[0], f[1], err)
		}
		if err := os.WriteFile(src, bytes.Replace(b, []byte(f[1]), []byte(f[2]), 1), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Join(stdDir, "pkg.mk")
	b, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	const list = "unsafe_modules: ["
	if !bytes.Contains(b, []byte(list)) {
		t.Fatalf("std/pkg.mk has no %q", list)
	}
	b = bytes.Replace(b, []byte(list), []byte(list+`"compress", `), 1)
	if err := os.WriteFile(pkg, b, 0o644); err != nil {
		t.Fatal(err)
	}
	exe := czBuildStd(tt, dir, stdDir, src, flags...)
	if c2Exes.m == nil {
		c2Exes.m = map[string]string{}
	}
	c2Exes.m[key] = exe
	return exe
}

// c2Pool holds one long-running batch program (records in, one czParse
// line out per record) for a test binary.
type c2Pool struct {
	sync.Mutex
	s *czServer
}

// c2Hang is how long one ask may wait for its answers before the program
// is killed and the input fails as a hang.
const c2Hang = 2 * time.Minute

// ask sends recs to the pool's program, starting it with start if none
// runs, and returns its answers. A program that dies, answers a line
// czParse refuses or does not answer within c2Hang fails the test and is
// replaced on the next ask.
func (p *c2Pool) ask(t *testing.T, what string, start func() *exec.Cmd, recs []czRec) []czRes {
	t.Helper()
	p.Lock()
	defer p.Unlock()
	if p.s == nil {
		cmd := start()
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		p.s = &czServer{cmd: cmd, in: in, out: bufio.NewReader(out), errb: &errb}
	}
	s := p.s
	hung := make(chan struct{})
	timer := time.AfterFunc(c2Hang, func() {
		close(hung)
		s.cmd.Process.Kill()
	})
	stop := func(format string, args ...any) {
		p.s = nil
		s.in.Close()
		s.cmd.Process.Kill()
		s.cmd.Wait()
		select {
		case <-hung:
			t.Fatalf("%s: no answer in %v, killed (a hang)\nstderr:\n%s", what, c2Hang, s.errb.String())
		default:
		}
		t.Fatalf(format+"\nstderr:\n%s", append(args, s.errb.String())...)
	}
	werr := make(chan error, 1)
	go func() {
		_, err := s.in.Write(czEncode(recs))
		werr <- err
	}()
	res := make([]czRes, 0, len(recs))
	for range recs {
		r, err := czParse(s.out)
		if err != nil {
			timer.Stop()
			stop("%s: %v", what, err)
		}
		res = append(res, r)
	}
	if !timer.Stop() {
		// Killed just after its last answer: start a new one next time.
		p.s = nil
		s.cmd.Wait()
		return res
	}
	if err := <-werr; err != nil {
		stop("%s died: %v", what, err)
	}
	return res
}

// c2Run runs exe with args over stdin and returns its stdout and stderr.
func c2Run(t *testing.T, exe string, stdin []byte, args ...string) ([]byte, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s %v: killed after 2 minutes", exe, args)
	}
	return out.Bytes(), errb.String(), err
}

// c2UnzlibModel is the Go model of Unzlib: the header by RFC 1950 section
// 2.2 (a truncated header, a CMF naming another method or a window over 32
// KiB at offset 0, a bad FCHECK or FDICT at offset 1), the body by
// compress/flate over a bytes.Reader, then the Adler-32 (corrupt at its
// offset) and no byte after it (corrupt there). A body's corrupt offset is
// -1 (unknown): Go's is its reader's position, not the bad element's.
func c2UnzlibModel(data []byte, limit int64) czRes {
	if len(data) < 1 {
		return czRes{verdict: "truncated"}
	}
	cmf := data[0]
	if cmf&0x0F != 8 || cmf>>4 > 7 {
		return czRes{verdict: "corrupt", offset: 0}
	}
	if len(data) < 2 {
		return czRes{verdict: "truncated"}
	}
	if (int(cmf)<<8|int(data[1]))%31 != 0 || data[1]&0x20 != 0 {
		return czRes{verdict: "corrupt", offset: 1}
	}
	br := bytes.NewReader(data[2:])
	out, err := io.ReadAll(flate.NewReader(br))
	if err != nil {
		var ce flate.CorruptInputError
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
			return czRes{verdict: "truncated"}
		case errors.As(err, &ce):
			return czRes{verdict: "corrupt", offset: -1}
		}
		return czRes{verdict: "error: " + err.Error()}
	}
	if int64(len(out)) > max(limit, 0) {
		return czRes{verdict: "toolarge"}
	}
	end := len(data) - br.Len()
	if len(data)-end < 4 {
		return czRes{verdict: "truncated"}
	}
	want := uint32(data[end])<<24 | uint32(data[end+1])<<16 | uint32(data[end+2])<<8 | uint32(data[end+3])
	if want != adler32.Checksum(out) {
		return czRes{verdict: "corrupt", offset: end}
	}
	if end+4 != len(data) {
		return czRes{verdict: "corrupt", offset: end + 4}
	}
	return czRes{verdict: "ok", n: len(out), crc: crc32.ChecksumIEEE(out)}
}

// c2Exact reports whether got equals want, offsets included unless want's
// offset is -1 (unknown).
func c2Exact(got, want czRes) bool {
	if !czSame(got, want) {
		return false
	}
	return got.verdict != "corrupt" || want.offset < 0 || got.offset == want.offset
}

// c2Line is a czRes as the stream program prints it.
func c2Line(r czRes) string {
	return r.String()
}

// c2Fmt formats records' names for a failure message.
func c2Fmt(name string, got, want czRes) string {
	return fmt.Sprintf("%s: got %v, want %v", name, got, want)
}
