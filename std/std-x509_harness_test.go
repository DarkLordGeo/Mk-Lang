// Why: thousands of std.x509 cases need one build, not one per case, and they
// reach private entries std does not export.
// What: x509Driver, x509Fixture, x509Exe, x509Ask and x509VerifyRecord: the Mk
// driver, the fixture that exports private entries, and the Go side feeding
// them.
// How: one record a line on standard input, one answer line each, judged
// against Go's crypto/x509; MK_X509_BASE builds over a snapshot of std.

package std

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// x509Driver is the Mk side. Every argument is lower-case hex or a
// decimal; every answer is "ok ..." or "err <the error as {e} prints it>".
const x509Driver = `import std.fs
import std.time
import std.x509

Nib(c: u8): u8 {
    if c >= b'a' {
        return c - 87
    }
    return c - 48
}

Unhex(a: &Arena, s: str): OutOfMemory![]u8 from a {
    b := s.AsBytes()
    out := try a.Slice[u8](b.Len() / 2)
    for i in 0..out.Len() {
        out[i] = (Nib(b[2 * i]) << 4) | Nib(b[2 * i + 1])
    }
    return out[..]
}

TextOf(a: &Arena, s: str): OutOfMemory!str from a {
    b := try Unhex(a, s)
    return str.From(b) else { "" }
}

// Kinds pins the kinds: Certificate and Pool are Plain, Send and Share.
Kinds[T: Plain + Send + Share](x: T): T {
    return x
}

Field(parts: &List[str], i: int): str {
    if i < parts.Len() {
        return parts[i]
    }
    return ""
}

Certs(a: &Arena, parts: &List[str], at: int, n: int): x509.Error!List[x509.Certificate] from a {
    var out := List[x509.Certificate].New(a)
    for i in 0..n {
        der := try Unhex(a, Field(parts, at + i))
        try out.Push(try x509.Parse(a, der))
    }
    return out
}

Some(a: &Arena, parts: &List[str], at: int, n: int, skip: int): OutOfMemory!List[x509.Certificate] from a {
    var out := List[x509.Certificate].New(a)
    for i in 0..n {
        der := try Unhex(a, Field(parts, at + i))
        c := x509.Parse(a, der) else {
            continue
        }
        if i >= skip {
            try out.Push(c)
        }
    }
    return out
}

Case(a: &Arena into a, io: Io, line: str into a): OutOfMemory!str from a {
    var parts := List[str].New(a)
    for p in line.Split(" ") {
        try parts.Push(p)
    }
    op := Field(&parts, 0)
    if op == "f" {
        path := try TextOf(a, Field(&parts, 1))
        text := fs.ReadText(a, io, path) else e {
            return try a.Fmt("err {e}", (e))
        }
        return try Case(a, io, text.Trim())
    }
    if op == "p" {
        der := try Unhex(a, Field(&parts, 1))
        c := x509.Parse(a, der) else e {
            return try a.Fmt("err {e}", (e))
        }
        d := Kinds(c)
        return try x509.FxSummary(a, &d)
    }
    if op == "m" {
        text := try TextOf(a, Field(&parts, 1))
        got := x509.ParsePem(a, text) else e {
            return try a.Fmt("err {e}", (e))
        }
        return try a.Fmt("ok {n}", (n: got.Len()))
    }
    if op == "w" {
        text := try TextOf(a, Field(&parts, 1))
        return try a.Fmt("ok {n}", (n: try x509.FxLenient(a, text)))
    }
    if op == "i" {
        text := try TextOf(a, Field(&parts, 1))
        return try x509.FxIp(a, text)
    }
    if op == "k" {
        der := try Unhex(a, Field(&parts, 1))
        c := x509.Parse(a, der) else e {
            return try a.Fmt("err {e}", (e))
        }
        k := c.PublicKey() else e {
            return try a.Fmt("err {e}", (e))
        }
        return match k {
            .Ec(point) { try a.Fmt("ok ec {n}", (n: point.Len())) }
            .Rsa { "ok rsa" }
            .Ed25519 { "ok ed25519" }
        }
    }
    if op == "u" {
        text := try TextOf(a, Field(&parts, 1))
        return try x509.FxUnique(a, text)
    }
    if op == "R" {
        path := try TextOf(a, Field(&parts, 1))
        limit := Field(&parts, 2).ParseInt() else { 0 }
        return x509.FxReadCapped(a, io, path, limit) else e {
            return try a.Fmt("err {e}", (e))
        }
    }
    if op == "r" {
        der := try Unhex(a, Field(&parts, 1))
        return try x509.FxMarkRefusals(a, io, der)
    }
    if op == "h" {
        host := try TextOf(a, Field(&parts, 1))
        der := try Unhex(a, Field(&parts, 2))
        c := x509.Parse(a, der) else e {
            return try a.Fmt("err {e}", (e))
        }
        return if x509.FxHost(&c, host) { "ok true" } else { "ok false" }
    }
    if op == "l" {
        now := Field(&parts, 1).ParseInt() else { 0 }
        host := try TextOf(a, Field(&parts, 2))
        n := Cast[int](Field(&parts, 3).ParseInt() else { 0 })
        leaf := try Unhex(a, Field(&parts, 4))
        first := x509.Parse(a, leaf) else e {
            return try a.Fmt("err {e}", (e))
        }
        var chain := List[x509.Certificate].New(a)
        try chain.Push(first)
        rest := try Some(a, &parts, 5, n - 1, 0)
        for i in 0..rest.Len() {
            try chain.Push(rest[i])
        }
        r := Cast[int](Field(&parts, 4 + n).ParseInt() else { 0 })
        roots := try Some(a, &parts, 5 + n, r, 0)
        rs := roots.Done()
        pool := Kinds(x509.Pool.New(rs[..]))
        cs := chain.Done()
        x509.Verify(a, io, cs[..], &pool, time.FromUnix(now, 0), host) else e {
            return try a.Fmt("err {e}", (e))
        }
        return "ok"
    }
    if op == "b" {
        now := Field(&parts, 1).ParseInt() else { 0 }
        n := Cast[int](Field(&parts, 2).ParseInt() else { 0 })
        chain := Certs(a, &parts, 3, n) else e {
            return try a.Fmt("err {e}", (e))
        }
        cs := chain.Done()
        return try x509.FxPlatformBits(a, io, cs[..], time.FromUnix(now, 0))
    }
    if op == "s" || op == "c" {
        now := Field(&parts, 1).ParseInt() else { 0 }
        host := try TextOf(a, Field(&parts, 2))
        n := Cast[int](Field(&parts, 3).ParseInt() else { 0 })
        chain := Certs(a, &parts, 4, n) else e {
            return try a.Fmt("err {e}", (e))
        }
        pool := x509.SystemRoots(a, io) else e {
            return try a.Fmt("err {e}", (e))
        }
        cs := chain.Done()
        if op == "c" {
            x509.FxVerifyCached(a, io, cs[..], &pool, time.FromUnix(now, 0), host) else e {
                return try a.Fmt("err {e}", (e))
            }
            return "ok"
        }
        x509.Verify(a, io, cs[..], &pool, time.FromUnix(now, 0), host) else e {
            return try a.Fmt("err {e}", (e))
        }
        return "ok"
    }
    if op == "v" {
        now := Field(&parts, 1).ParseInt() else { 0 }
        host := try TextOf(a, Field(&parts, 2))
        n := Field(&parts, 3).ParseInt() else { 0 }
        chain := Certs(a, &parts, 4, Cast[int](n)) else e {
            return try a.Fmt("err {e}", (e))
        }
        at := 4 + Cast[int](n)
        r := Field(&parts, at).ParseInt() else { 0 }
        roots := Certs(a, &parts, at + 1, Cast[int](r)) else e {
            return try a.Fmt("err {e}", (e))
        }
        rs := roots.Done()
        pool := x509.Pool.New(rs[..])
        cs := chain.Done()
        x509.Verify(a, io, cs[..], &pool, time.FromUnix(now, 0), host) else e {
            return try a.Fmt("err {e}", (e))
        }
        return "ok"
    }
    return "err unknown op"
}

// OneLine is s with each line break written as \n or \r, so an answer
// that holds a name with one stays one line.
OneLine(a: &Arena, s: str): OutOfMemory!str from a, s {
    b := s.AsBytes()
    var t := Text.New(a)
    var from := 0
    for i in 0..b.Len() {
        if b[i] == b'\n' || b[i] == b'\r' {
            try t.Push(s[from..i])
            try t.Push(if b[i] == b'\n' { "\\n" } else { "\\r" })
            from = i + 1
        }
    }
    if from == 0 {
        return s
    }
    try t.Push(s[from..])
    return t.Done()
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var r := sys.Stdin()
    var out := sys.Stdout()
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    for {
        line := try r.Line(&arena) else {
            break
        }
        answer := try Case(&arena, sys.io, line)
        try out.Str(try OneLine(&arena, answer))
        try out.Str("\n")
        arena.Reset()
    }
    try out.Flush()
}
`

// x509Fixture is written to std/x509 in the driver's copy of std.
const x509Fixture = `// Fixture of the std-x509 tests: private entries, exported.

import std.core
import std.rt
import std.time

/// FxSummary writes what Parse recorded, for the differential.
export FxSummary(a: &Arena, c: &Certificate): OutOfMemory!str from a *c into a {
    subject := try c.Subject(a)
    issuer := try c.Issuer(a)
    names := try c.Names(a)
    var t := Text.New(a)
    for i in 0..names.Len() {
        if i > 0 {
            try t.Push(",")
        }
        try t.Push(names[i])
    }
    nb := c.NotBefore().Unix()
    na := c.NotAfter().Unix()
    return try a.Fmt("ok {subject}|{issuer}|{names}|{nb}|{na}|{flags}|{pl}|{ku}|{alg}|{san}|{nc}", (subject, issuer, names: t.Str(), nb, na, flags: c.flags, pl: c.path_len, ku: c.key_usage, alg: c.sig_alg, san: c.San().Len(), nc: c.Permitted().Len() + c.Excluded().Len()))
}

/// FxLenient is how many certificates the system bundle's reader takes
/// from text.
export FxLenient(a: &Arena, text: str): OutOfMemory!int {
    var out := List[Certificate].New(a)
    PemCertificates(a, text, false, mut out) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            _ {}
        }
    }
    return out.Len()
}

/// FxIp is IpOf's address in hex, or "none".
export FxIp(a: &Arena, text: str): OutOfMemory!str from a {
    ip := IpOf(text.AsBytes()) else {
        return "none"
    }
    var t := Text.New(a)
    for x in ip.b {
        try t.Push(HexDigits[int(x >> 4)..int(x >> 4) + 1])
        try t.Push(HexDigits[int(x & 15)..int(x & 15) + 1])
    }
    return try a.Fmt("ok {h}", (h: t.Str()))
}

/// FxHost is HostMatches.
export FxHost(c: &Certificate, host: str): bool {
    return HostMatches(c, host)
}

/// FxVerifyCached is Verify with the platform's retrieval from its local
/// cache only (the Windows tests touch no network).
export FxVerifyCached(a: &Arena, io: Io, chain: []Certificate, roots: &Pool, now: time.Time, host: str): Error!void {
    try VerifyWith(a, io, chain, roots, now, host, true)
}

/// FxMarkRefusals is rt.CertVerify given the cache-only mark and a valid
/// record, rt.CertVerifyCached given the record without the mark, and
/// rt.CertVerifyCached given both: the first two are malformed requests.
export FxMarkRefusals(a: &Arena, io: Io, der: []u8): OutOfMemory!str from a {
    k := der.Len()
    req := try a.Slice[u8](8 + k)
    for i in 0..4 {
        req[i] = 0
    }
    req[4] = Trunc[u8](k)
    req[5] = Trunc[u8](k >> 8)
    req[6] = Trunc[u8](k >> 16)
    req[7] = Trunc[u8](k >> 24)
    (mut req[8..8 + k]).CopyFrom(der)
    marked := rt.CertVerify(io, req[..], "", 0)
    plain := rt.CertVerifyCached(io, req[4..], "", 0)
    both := rt.CertVerifyCached(io, req[..], "", 0)
    return try a.Fmt("ok {m} {p} {b}", (m: FxErr(marked.err), p: FxErr(plain.err), b: FxErr(both.err)))
}

/// FxPlatformBits is the platform's raw answer for chain at now, with
/// retrieval from its cache only (the platform tests): "ok <verdict> <status>",
/// both in hex, or "err <SysErr>".
export FxPlatformBits(a: &Arena, io: Io, chain: []Certificate, now: time.Time): OutOfMemory!str from a {
    var total := 4
    for i in 0..chain.Len() {
        total += 4 + chain[i].data.Len()
    }
    req := try a.Slice[u8](total)
    for i in 0..4 {
        req[i] = 0
    }
    var at := 4
    for i in 0..chain.Len() {
        d := chain[i].data
        k := d.Len()
        req[at] = Trunc[u8](k)
        req[at + 1] = Trunc[u8](k >> 8)
        req[at + 2] = Trunc[u8](k >> 16)
        req[at + 3] = Trunc[u8](k >> 24)
        (mut req[at + 4..at + 4 + k]).CopyFrom(d)
        at += 4 + k
    }
    r := rt.CertVerifyCached(io, req[..], "", now.UnixNanos())
    if r.err != core.SysErr.None {
        return try a.Fmt("err {e}", (e: FxErr(r.err)))
    }
    return try a.Fmt("ok {v:x} {o:x}", (v: r.value, o: r.os))
}

// FxErr names the SysErr values the refusals give.
FxErr(e: core.SysErr): str {
    if e == core.SysErr.None {
        return "None"
    }
    if e == core.SysErr.Other {
        return "Other"
    }
    if e == core.SysErr.Unsupported {
        return "Unsupported"
    }
    return "another"
}

/// FxUnique is the subjects of the certificates of text's blocks after
/// SystemRoots' Unique, joined by '|'.
export FxUnique(a: &Arena, text: str): OutOfMemory!str from a {
    var out := List[Certificate].New(a)
    PemCertificates(a, text, false, mut out) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            _ {}
        }
    }
    all := out.Done()
    k := try Unique(a, all)
    u := all[0..k]
    var t := Text.New(a)
    for i in 0..u.Len() {
        if i > 0 {
            try t.Push("|")
        }
        try t.Push(try u[i].Subject(a))
    }
    return try a.Fmt("ok {s}", (s: t.Str()))
}

/// FxReadCapped is the length ReadCapped reads from p under limit, or
/// "none".
export FxReadCapped(a: &Arena, io: Io, p: str, limit: i64): Canceled|OutOfMemory!str from a {
    got := try ReadCapped(a, io, p, limit)
    b := got else {
        return "ok none"
    }
    return try a.Fmt("ok {n}", (n: b.Len()))
}

/// FxNow is a fixed instant for tests.
export FxNow(s: i64): time.Time {
    return time.FromUnix(s, 0)
}
`

// x509Built holds one driver executable per build mode.
var x509Built struct {
	sync.Mutex
	exe map[string]string
	err map[string]string
}

// x509StdDir builds the std directory the driver's mk uses: the live
// tree, or MK_X509_BASE with this tree's std/x509; both with the fixture.
// The snapshot exists because mk checks all of std: a module edited
// elsewhere while a run is live, and not checking, would stop every mk
// command.
func x509StdDir(t *testing.T, root string) string {
	t.Helper()
	fx := map[string]string{"x509/tests_x509fx.mk": x509Fixture}
	base := os.Getenv("MK_X509_BASE")
	if base == "" {
		return copyStd(t, root, fx)
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "x509")); err != nil {
		t.Fatal(err)
	}
	for _, m := range mkFilesIn(t, "x509") {
		dst := filepath.Join(stdDir, "x509", filepath.Base(m))
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stdDir, "x509", "tests_x509fx.mk"), []byte(x509Fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return stdDir
}

// mkFilesIn lists the .mk files of one std directory on disk.
func mkFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// x509Exe builds the driver for mode ("dev" or "release") once per test
// binary and returns its path; a missing toolchain skips the test.
func x509Exe(t testing.TB, mode string) string {
	t.Helper()
	x509Built.Lock()
	defer x509Built.Unlock()
	if x509Built.exe == nil {
		x509Built.exe = map[string]string{}
		x509Built.err = map[string]string{}
	}
	if exe, ok := x509Built.exe[mode]; ok {
		return exe
	}
	if msg, ok := x509Built.err[mode]; ok {
		t.Skip(msg)
	}
	tt, ok := t.(*testing.T)
	if !ok {
		t.Fatal("x509Exe: the first build needs a *testing.T")
	}
	if old, _ := filepath.Glob(filepath.Join(os.TempDir(), "mk-x509-*")); len(old) > 0 {
		for _, d := range old {
			if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > 2*time.Hour {
				os.RemoveAll(d)
			}
		}
	}
	dir, err := packageTempDir("mk-x509-" + mode)
	if err != nil {
		t.Fatal(err)
	}
	stdDir := x509StdDir(tt, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(x509Driver), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build"}
	if mode == "release" {
		args = append(args, "--release")
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(tt, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			x509Built.err[mode] = "needs the LLVM toolchain: " + out
			t.Skip(x509Built.err[mode])
		}
		t.Fatalf("mk build of the x509 driver (%s): exit %d\n%s", mode, status, out)
	}
	// Keep only the executable: the std copy and its build cache are
	// hundreds of MB, and every test binary (each fuzz worker too) builds
	// its own driver, so leaving them filled the disk (2026-11-13: 40 GB).
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if p := filepath.Join(dir, e.Name()); p != exe {
				os.RemoveAll(p)
			}
		}
	}
	x509Built.exe[mode] = exe
	return exe
}

// x509Modes are the builds every differential runs.
var x509Modes = []string{"dev", "release"}

// x509Ask sends records to the mode's driver and returns its answers, one
// per record.
func x509Ask(t *testing.T, mode string, records []string) []string {
	t.Helper()
	exe := x509Exe(t, mode)
	var got []string
	const batch = 2000
	for start := 0; start < len(records); start += batch {
		end := min(start+batch, len(records))
		var in strings.Builder
		for i, r := range records[start:end] {
			// A Stdin line is at most 64 KiB: a longer record goes by file.
			if len(r) > 60000 {
				p := filepath.Join(t.TempDir(), fmt.Sprintf("record%d.txt", start+i))
				if err := os.WriteFile(p, []byte(r), 0o644); err != nil {
					t.Fatal(err)
				}
				r = "f " + x509Hex([]byte(p))
			}
			in.WriteString(r)
			in.WriteString("\n")
		}
		r := runNative(t, exe, in.String())
		if r.code != 0 {
			t.Fatalf("driver (%s) exit %d\nstderr: %s\nlast output: %s", mode, r.code, r.stderr, encTail(r.stdout))
		}
		lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(lines) != end-start {
			t.Fatalf("driver (%s): %d answers for %d records\nstderr: %s", mode, len(lines), end-start, r.stderr)
		}
		got = append(got, lines...)
	}
	return got
}

// x509Hex is the driver's hex of b.
func x509Hex(b []byte) string { return hex.EncodeToString(b) }

// x509VerifyRecord is a "v" record: chain and roots at now for host.
func x509VerifyRecord(now time.Time, host string, chain, roots [][]byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "v %d %s %d", now.Unix(), x509Hex([]byte(host)), len(chain))
	for _, c := range chain {
		b.WriteString(" " + x509Hex(c))
	}
	fmt.Fprintf(&b, " %d", len(roots))
	for _, c := range roots {
		b.WriteString(" " + x509Hex(c))
	}
	return b.String()
}
