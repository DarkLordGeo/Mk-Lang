// Why: x509.Verify and x509.SystemRoots must be repeatable under cancellation,
// and SystemRoots on Linux must read the store as Go does.
// What: TestX509_CancelClasses, TestX509_CancelEachAwait,
// TestX509_SystemRootsLinux and TestX509_SystemRootsLinksLinux.
// How: cancelled calls under std.sim and natively, then an uncut call compared;
// on Linux (MK_LINUX=1, WSL lock) the subjects are compared with Go's
// SystemCertPool.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// x509CancelProg is the Mk program; %s are the chain's DER in hex (space
// separated), the root's DER in hex, and the root, the RSA root and the
// Ed25519 root as PEM.
const x509CancelProg = `import std.atomic
import std.fs
import std.sim
import std.time
import std.x509

const ChainHex: str = "%s"
const RootHex: str = "%s"
const RootPem: str = "%s"
const RsaPem: str = "%s"
const EdPem: str = "%s"
const Host: str = "example.test"

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

Certs(a: &Arena, hexes: str): x509.Error!List[x509.Certificate] from a {
    var out := List[x509.Certificate].New(a)
    for h in hexes.Split(" ") {
        try out.Push(try x509.Parse(a, try Unhex(a, h)))
    }
    return out
}

Now(): time.Time {
    return time.FromUnix(1_704_067_200, 0)
}

// Check is Verify of chain against pool at Now for Host, as text; cached
// is the module's cache-only entry.
Check(a: &Arena, io: Io, chain: []x509.Certificate, pool: &x509.Pool, cached: bool): OutOfMemory!str from a {
    if cached {
        x509.FxVerifyCached(a, io, chain, pool, Now(), Host) else e {
            return try a.Fmt("{e}", (e))
        }
        return "ok"
    }
    x509.Verify(a, io, chain, pool, Now(), Host) else e {
        return try a.Fmt("{e}", (e))
    }
    return "ok"
}

// Attempt is SystemRoots, then Check against it.
Attempt(a: &Arena, io: Io, chain: []x509.Certificate, cached: bool): OutOfMemory!str from a {
    pool := x509.SystemRoots(a, io) else e {
        return try a.Fmt("roots {e}", (e))
    }
    return try Check(a, io, chain, &pool, cached)
}

// Subjects is SystemRoots' subjects joined by '|', or its error.
Subjects(a: &Arena, io: Io): OutOfMemory!str from a {
    pool := x509.SystemRoots(a, io) else e {
        return try a.Fmt("err {e}", (e))
    }
    var t := Text.New(a)
    for i in 0..pool.certs.Len() {
        if i > 0 {
            try t.Push("|")
        }
        try t.Push(pool.certs[i].Subject(a) else { "?" })
    }
    return try a.Dup(t.Str())
}

// SimFiles writes the sweep's disk: a bundle of the root, and three files
// of /etc/ssl/certs: the Ed25519 root, the RSA root and the root again
// (the bundle, in the same directory, is read twice too; Unique drops
// both copies).
SimFiles(io: Io): bool {
    fs.MakeDirAll(io, "/etc/ssl/certs") else {
        return false
    }
    fs.WriteAll(io, "/etc/ssl/certs/ca-certificates.crt", RootPem.AsBytes()) else {
        return false
    }
    fs.WriteAll(io, "/etc/ssl/certs/a.pem", EdPem.AsBytes()) else {
        return false
    }
    fs.WriteAll(io, "/etc/ssl/certs/b.pem", RsaPem.AsBytes()) else {
        return false
    }
    fs.WriteAll(io, "/etc/ssl/certs/c.pem", RootPem.AsBytes()) else {
        return false
    }
    return true
}

// Fnv is the FNV-1a hash of s, as a non-negative number.
Fnv(s: str): i64 {
    var x: u64 = 14695981039346656037
    for b in s.AsBytes() {
        x = (x ^ Cast[u64](b)) *%% 1099511628211
    }
    return Cast[i64](x >> 1)
}

// RootsTask runs SystemRoots in an arena of its own and stores in out
// Fnv of its subjects (-1 for Canceled, -2 for another error), and in end
// the simulated nanoseconds from start to its return. A task, so each
// disk wait is an event of the Sim and a deadline fires inside it.
RootsTask(io: Io, h: &Heap, start: time.Instant, out: &atomic.AtomicInt, end: &atomic.AtomicInt) {
    var a := Arena.New(h) else {
        out.Store(-3)
        return
    }
    defer a.Free()
    text := Subjects(&a, io) else { "oom" }
    end.Store(time.Now(io).Since(start).Nanos())
    if text == "err Canceled" {
        out.Store(-1)
    } else if text.Len() >= 4 && text[0..4] == "err " {
        out.Store(-2)
    } else {
        out.Store(Fnv(text))
    }
}

// SweepOnce runs SystemRoots in a task in a fresh Sim of seed 7 (so every
// run meets the same latencies), cancelled d microseconds after it starts
// when d >= 0, then again uninterrupted; it writes "<d> <us to return>
// <first> || <second>", each a hash of the subjects or -1 for Canceled,
// and returns the microseconds.
SweepOnce(a: &Arena, h: &Heap, w: mut Writer, d: i64): WriteError|OutOfMemory!i64 {
    var sm := try sim.Sim.New(a, 7)
    defer sm.Free()
    io := sm.Io()
    if !SimFiles(io) {
        try Line(w, "sweep", "files not written")
        return 0
    }
    out := atomic.AtomicInt.New(0)
    end := atomic.AtomicInt.New(0)
    start := time.Now(io)
    scope s := io.Scope() {
        if d >= 0 {
            s.CancelAfter(time.Micros(d))
        }
        s.go RootsTask(io, h, start, &out, &end)
    }
    took := end.Load() / 1_000
    again := try Subjects(a, io)
    if d < 0 {
        try Line(w, "subjects", again)
    }
    try Line(w, "sweep", try a.Fmt("{d} {took} {got} || {again}", (d, took, got: out.Load(), again: Fnv(again))))
    return took
}

// Sweep is the cancel-safety sweep of SystemRoots under std.sim: one uninterrupted
// run, then one cancelled at every microsecond up to its length, each
// followed by an uninterrupted call.
Sweep(a: &Arena, h: &Heap, w: mut Writer): WriteError|OutOfMemory!void {
    total := try SweepOnce(a, h, w, -1)
    for d in 0..total + 2 {
        var it := try a.Sub()
        defer it.Free()
        _ = try SweepOnce(&it, h, w, d)
    }
}

// MakeLinks makes directory d with the Ed25519 root, the root again, a
// link to the first beside it and a link to /dev/zero.
MakeLinks(a: &Arena, io: Io, d: str): OutOfMemory!str from a {
    fs.RemoveAll(io, d) else {}
    fs.MakeDirAll(io, d) else e {
        return try a.Fmt("mkdir {e}", (e))
    }
    fs.WriteAll(io, try a.Fmt("{d}/ed.pem", (d)), EdPem.AsBytes()) else e {
        return try a.Fmt("write {e}", (e))
    }
    fs.WriteAll(io, try a.Fmt("{d}/dup.pem", (d)), RootPem.AsBytes()) else e {
        return try a.Fmt("write {e}", (e))
    }
    fs.Symlink(io, "ed.pem", try a.Fmt("{d}/h.0", (d))) else e {
        return try a.Fmt("link {e}", (e))
    }
    fs.Symlink(io, "/dev/zero", try a.Fmt("{d}/zero.pem", (d))) else e {
        return try a.Fmt("link {e}", (e))
    }
    return "made"
}

// MakeOrder makes directory d with c.pem (the root), a.pem (the RSA root)
// and b.pem (the Ed25519 root), in that order, so a listing in creation
// order or its reverse is not sorted (X509-R2-5).
MakeOrder(a: &Arena, io: Io, d: str): OutOfMemory!str from a {
    fs.RemoveAll(io, d) else {}
    fs.MakeDirAll(io, d) else e {
        return try a.Fmt("mkdir {e}", (e))
    }
    fs.WriteAll(io, try a.Fmt("{d}/c.pem", (d)), RootPem.AsBytes()) else e {
        return try a.Fmt("write {e}", (e))
    }
    fs.WriteAll(io, try a.Fmt("{d}/a.pem", (d)), RsaPem.AsBytes()) else e {
        return try a.Fmt("write {e}", (e))
    }
    fs.WriteAll(io, try a.Fmt("{d}/b.pem", (d)), EdPem.AsBytes()) else e {
        return try a.Fmt("write {e}", (e))
    }
    return "made"
}

Line(w: mut Writer, name: str, v: str): WriteError!void {
    try w.Str(name)
    try w.Str(" ")
    try w.Str(v)
    try w.Str("\n")
}

// Roots writes the subject of every certificate of the system's pool.
Roots(a: &Arena, io: Io, w: mut Writer): WriteError|OutOfMemory!void {
    pool := x509.SystemRoots(a, io) else e {
        try Line(w, "roots", try a.Fmt("{e}", (e)))
        return
    }
    for i in 0..pool.certs.Len() {
        s := pool.certs[i].Subject(a) else {
            try Line(w, "subject", "?")
            continue
        }
        try Line(w, "subject", s)
    }
    try Line(w, "count", try a.Fmt("{n}", (n: pool.certs.Len())))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    a := sys.arena
    io := sys.io
    links := sys.Env("MK_X509_MKLINKS") else { "" }
    if links.Len() > 0 {
        try Line(mut out, "links", try MakeLinks(a, io, links))
        try out.Flush()
        return
    }
    order := sys.Env("MK_X509_MKORDER") else { "" }
    if order.Len() > 0 {
        try Line(mut out, "order", try MakeOrder(a, io, order))
        try out.Flush()
        return
    }
    gone := sys.Env("MK_X509_RMDIR") else { "" }
    if gone.Len() > 0 {
        fs.RemoveAll(io, gone) else {}
        try out.Flush()
        return
    }
    if (sys.Env("MK_X509_SWEEP") else { "" }) == "1" {
        try Sweep(a, sys.heap, mut out)
        try out.Flush()
        return
    }
    if (sys.Env("MK_X509_ROOTS") else { "" }) == "1" {
        try Roots(a, io, mut out)
        try out.Flush()
        return
    }
    list := Certs(a, ChainHex) else e {
        try Line(mut out, "chain", try a.Fmt("{e}", (e)))
        try out.Flush()
        return
    }
    rl := Certs(a, RootHex) else e {
        try Line(mut out, "root", try a.Fmt("{e}", (e)))
        try out.Flush()
        return
    }
    cs := list.Done()
    rs := rl.Done()
    own := x509.Pool.New(rs[..])
    var system := "unset"
    var mine := "unset"
    scope sc := io.Scope() {
        sc.Cancel()
        system = Attempt(a, io, cs[..], true) else { "oom" }
        mine = Check(a, io, cs[..], &own, false) else { "oom" }
    }
    try Line(mut out, "native-cancelled-system", system)
    try Line(mut out, "native-system", try Attempt(a, io, cs[..], true))
    try Line(mut out, "native-cancelled-own", mine)
    try Line(mut out, "native-own", try Check(a, io, cs[..], &own, false))
    var sm := try sim.Sim.New(a, 7)
    defer sm.Free()
    sio := sm.Io()
    var sim_cancelled := "unset"
    scope sc := sio.Scope() {
        sc.Cancel()
        sim_cancelled = Attempt(a, sio, cs[..], false) else { "oom" }
    }
    try Line(mut out, "sim-cancelled-system", sim_cancelled)
    try Line(mut out, "sim-system", try Attempt(a, sio, cs[..], false))
    try Line(mut out, "sim-own", try Check(a, sio, cs[..], &own, false))
    if Target.os == Os.Linux {
        var bundle := "written"
        fs.MakeDirAll(sio, "/etc/ssl/certs") else e {
            bundle = try a.Fmt("mkdir {e}", (e))
        }
        fs.WriteAll(sio, "/etc/ssl/certs/ca-certificates.crt", RootPem.AsBytes()) else e {
            bundle = try a.Fmt("write {e}", (e))
        }
        try Line(mut out, "sim-bundle-file", bundle)
        try Line(mut out, "sim-bundle", try Attempt(a, sio, cs[..], false))
    }
    try out.Flush()
}
`

// x509CancelSrc is x509CancelProg over the fixtures.
func x509CancelSrc(t *testing.T) string {
	t.Helper()
	fx := x509Fixtures(t)
	var chain []string
	for _, d := range fx["chain.pem"] {
		chain = append(chain, hex.EncodeToString(d))
	}
	root := fx["root.pem"][0]
	text := func(der []byte) string {
		p := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		return strings.ReplaceAll(p, "\n", `\n`)
	}
	return fmt.Sprintf(x509CancelProg, strings.Join(chain, " "), hex.EncodeToString(root), text(root), text(fx["rsa-root.pem"][0]), text(fx["ed-root.pem"][0]))
}

// x509CancelWant is each line's answer, by target.
func x509CancelWant(linux bool) map[string]string {
	if linux {
		return map[string]string{
			"native-cancelled-system": "roots Canceled",
			"native-system":           "UnknownAuthority",
			"native-cancelled-own":    "ok",
			"native-own":              "ok",
			"sim-cancelled-system":    "roots Canceled",
			"sim-system":              "UnknownAuthority",
			"sim-own":                 "ok",
			"sim-bundle-file":         "written",
			"sim-bundle":              "ok",
		}
	}
	return map[string]string{
		"native-cancelled-system": "Canceled",
		"native-system":           "UnknownAuthority",
		"native-cancelled-own":    "ok",
		"native-own":              "ok",
		"sim-cancelled-system":    "Unsupported",
		"sim-system":              "Unsupported",
		"sim-own":                 "ok",
	}
}

// x509Lines reads "name value" lines.
func x509Lines(out string) map[string]string {
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		k, v, _ := strings.Cut(l, " ")
		got[k] = v
	}
	return got
}

// TestX509_CancelClasses: the cancel-safety classes of Verify and SystemRoots,
// natively and under std.sim, on the host and on x86_64-linux when
// MK_LINUX=1. Both are Repeatable. Verify with the program's own pool takes
// no wait, so a cancel does not change its answer; against the system's
// pool on Windows it fails with Canceled before the platform's check starts
// (rt.CertVerify is whole once started), and the next call answers as an
// uninterrupted one. SystemRoots on Linux fails with Canceled at the first
// read under a cancelled scope and consumes nothing. Under std.sim the
// platform's check is Unsupported (a Sim has no OS) and SystemRoots on
// Linux reads the simulated disk; the sim-cancelled line pins both.
func TestX509_CancelClasses(t *testing.T) {
	src := x509CancelSrc(t)
	for _, h := range stdLogHosts() {
		t.Run(h.name(), func(t *testing.T) {
			exe := h.build(t, map[string]string{"x509/tests_x509fx.mk": x509Fixture}, src)
			r := h.run(t, exe, "")
			if r.code != 0 {
				t.Fatalf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
			}
			got := x509Lines(r.stdout)
			want := x509CancelWant(h.linux)
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s: got %q, want %q", k, got[k], v)
				}
			}
			if len(got) != len(want) {
				t.Errorf("%d lines, want %d:\n%s", len(got), len(want), r.stdout)
			}
		})
	}
}

// x509GoRoots is the Go oracle run on Linux: the subjects of
// x509.SystemCertPool.
const x509GoRoots = `package main

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
)

func main() {
	p, err := x509.SystemCertPool()
	if err != nil {
		fmt.Println("roots", err)
		return
	}
	//lint:ignore SA1019 a pool from files on Linux lists its subjects
	for _, s := range p.Subjects() {
		var r pkix.RDNSequence
		if _, err := asn1.Unmarshal(s, &r); err != nil {
			fmt.Println("subject ?")
			continue
		}
		fmt.Println("subject", r.String())
	}
}
`

// x509Subjects is the sorted set of "subject" lines of out.
func x509Subjects(out string) []string {
	seen := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(l, "subject "); ok {
			seen[s] = true
		}
	}
	var all []string
	for s := range seen {
		all = append(all, s)
	}
	slices.Sort(all)
	return all
}

// TestX509_SystemRootsLinux: SystemRoots on x86_64-linux against Go's
// SystemCertPool, with the fixtures named by SSL_CERT_FILE and
// SSL_CERT_DIR, and with the distribution's bundle. MK_LINUX=1 only.
func TestX509_SystemRootsLinux(t *testing.T) {
	if !linuxRun() {
		t.Skip("MK_LINUX=1 runs the x86_64-linux leaf (under the WSL lock, -p 1)")
	}
	exe := linuxIo.build(t, map[string]string{"x509/tests_x509fx.mk": x509Fixture}, x509CancelSrc(t))
	dir := filepath.Dir(exe)
	gox := x509BuildGoRoots(t, dir)
	fx := x509Fixtures(t)
	var bundle []byte
	for _, f := range []string{"root.pem", "rsa-root.pem"} {
		bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fx[f][0]})...)
	}
	// A broken block between them is skipped by both.
	bundle = append(bundle, "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"...)
	if err := os.WriteFile(filepath.Join(dir, "bundle.pem"), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "certdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	ed := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fx["ed-root.pem"][0]})
	if err := os.WriteFile(filepath.Join(dir, "certdir", "ed.pem"), ed, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		env  []string
		min  int
	}{
		{"fixtures", []string{"SSL_CERT_FILE=bundle.pem", "SSL_CERT_DIR=certdir"}, 3},
		{"file only", []string{"SSL_CERT_FILE=bundle.pem", "SSL_CERT_DIR=nonexistent"}, 2},
		{"distribution", nil, 1},
	}
	var mkCalls, goCalls []ioCall
	for _, c := range cases {
		mkCalls = append(mkCalls, ioCall{env: append([]string{"MK_X509_ROOTS=1"}, c.env...), watch: 60})
		goCalls = append(goCalls, ioCall{env: c.env, watch: 60})
	}
	mk := linuxIo.runMany(t, exe, mkCalls)
	gor := linuxIo.runMany(t, gox, goCalls)
	for i, c := range cases {
		if mk[i].code != 0 || gor[i].code != 0 {
			t.Errorf("%s: exit %d (mk), %d (go)\n%s%s", c.name, mk[i].code, gor[i].code, mk[i].stderr, gor[i].stderr)
			continue
		}
		m, g := x509Subjects(mk[i].stdout), x509Subjects(gor[i].stdout)
		if len(m) < c.min {
			t.Errorf("%s: %d subjects, want at least %d\n%s", c.name, len(m), c.min, mk[i].stdout)
		}
		if !slices.Equal(m, g) {
			t.Errorf("%s: subjects differ\nmk only: %v\ngo only: %v", c.name, x509Minus(m, g), x509Minus(g, m))
		} else if ms, gs := x509SubjectSeq(mk[i].stdout), x509SubjectSeq(gor[i].stdout); !slices.Equal(ms, gs) {
			// The same order as Go's pool, not only the same set.
			t.Errorf("%s: the same subjects in another order\nmk: %q\ngo: %q", c.name, ms, gs)
		}
		t.Logf("%s: %d subjects (%s)", c.name, len(m), strings.TrimSpace(mk[i].stdout[strings.LastIndex(mk[i].stdout, "count"):]))
	}
}

// x509Minus is a without b's members.
func x509Minus(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// TestX509_CancelEachAwait: the cancel sweep for SystemRoots on x86_64-linux
// under std.sim. A bundle and three files of /etc/ssl/certs (two
// of them the bundle's roots again) are read by an uninterrupted
// SystemRoots, then by one cancelled at every microsecond up to that
// run's length (the disk's latency floor is 1 us, so every await point is
// reached by some cut), each in a fresh Sim of the same seed, so every
// run meets the same latencies. Each interrupted call answers Canceled or
// the whole pool, and the call after it the same subjects in the same
// order as the uninterrupted run. MK_LINUX=1 only.
func TestX509_CancelEachAwait(t *testing.T) {
	if !linuxRun() {
		t.Skip("MK_LINUX=1 runs the x86_64-linux leaf (under the WSL lock, -p 1)")
	}
	exe := linuxIo.build(t, map[string]string{"x509/tests_x509fx.mk": x509Fixture}, x509CancelSrc(t))
	r := linuxIo.runMany(t, exe, []ioCall{{env: []string{"MK_X509_SWEEP=1"}, watch: 120}})[0]
	if r.code != 0 {
		t.Fatalf("exit %d\n%s%s", r.code, encTail(r.stdout), r.stderr)
	}
	lines := stdLogLines(r.stdout)
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "subjects ") {
		t.Fatalf("not a sweep:\n%s", encTail(r.stdout))
	}
	subjects := strings.TrimPrefix(lines[0], "subjects ")
	if strings.Count(subjects, "|") != 2 {
		t.Fatalf("uninterrupted run: %q, want three subjects", subjects)
	}
	parse := func(l string) (d, took int, first, again string) {
		body, ok := strings.CutPrefix(l, "sweep ")
		if !ok {
			t.Fatalf("not a sweep line: %q", l)
		}
		f := strings.Fields(body)
		if len(f) != 5 || f[3] != "||" {
			t.Fatalf("not a sweep line: %q", l)
		}
		fmt.Sscan(f[0], &d)
		fmt.Sscan(f[1], &took)
		return d, took, f[2], f[4]
	}
	_, total, ref, refAgain := parse(lines[1])
	if strings.HasPrefix(ref, "-") || ref != refAgain {
		t.Fatalf("uninterrupted run: %s then %s, want one hash twice", ref, refAgain)
	}
	cuts, whole := 0, 0
	points := map[int]bool{}
	for _, l := range lines[2:] {
		d, took, first, again := parse(l)
		switch first {
		case "-1":
			cuts++
			points[took] = true
		case ref:
			whole++
		default:
			t.Errorf("cut at %d us: %s, want Canceled (-1) or the whole pool (%s)", d, first, ref)
		}
		if again != ref {
			t.Errorf("cut at %d us: the next call gives %s, want %s", d, again, ref)
		}
	}
	t.Logf("uninterrupted: %d us, %q; %d runs: %d Canceled at %d distinct return times, %d whole", total, subjects, len(lines)-2, cuts, len(points), whole)
	if len(points) < 15 {
		t.Errorf("only %d distinct interruption points; want one per await (at least 15)", len(points))
	}
}

// TestX509_SystemRootsLinksLinux: on x86_64-linux, SSL_CERT_DIR holding the
// Ed25519 root, the bundle's root again, a link to the first in the same
// directory, and a link to /dev/zero gives each root once, and
// the /dev/zero link, whose size reads 0, is read only to MaxBundle and
// skipped. Go's SystemCertPool is not run here: it reads
// /dev/zero until memory runs out. MK_LINUX=1 only.
func TestX509_SystemRootsLinksLinux(t *testing.T) {
	if !linuxRun() {
		t.Skip("MK_LINUX=1 runs the x86_64-linux leaf (under the WSL lock, -p 1)")
	}
	exe := linuxIo.build(t, map[string]string{"x509/tests_x509fx.mk": x509Fixture}, x509CancelSrc(t))
	dir := filepath.Dir(exe)
	fx := x509Fixtures(t)
	var bundle []byte
	for _, f := range []string{"root.pem", "rsa-root.pem"} {
		bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fx[f][0]})...)
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.pem"), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	links := fmt.Sprintf("/tmp/mk-x509-links-%d", os.Getpid())
	r := linuxIo.runMany(t, exe, []ioCall{
		{env: []string{"MK_X509_MKLINKS=" + links}, watch: 60},
		{env: []string{"MK_X509_ROOTS=1", "SSL_CERT_FILE=bundle.pem", "SSL_CERT_DIR=" + links}, watch: 60},
	})
	if r[0].code != 0 || strings.TrimSpace(r[0].stdout) != "links made" {
		t.Fatalf("making the links: exit %d\n%s%s", r[0].code, r[0].stdout, r[0].stderr)
	}
	if r[1].code != 0 {
		t.Fatalf("exit %d\n%s%s", r[1].code, r[1].stdout, r[1].stderr)
	}
	var want []string
	for _, f := range []string{"root.pem", "rsa-root.pem", "ed-root.pem"} {
		want = append(want, x509NameText(x509MustParse(t, fx[f][0]).RawSubject))
	}
	slices.Sort(want)
	if got := x509Subjects(r[1].stdout); !slices.Equal(got, want) {
		t.Errorf("subjects %q, want %q", got, want)
	}
	if !strings.Contains(r[1].stdout, "count 3") {
		t.Errorf("want each root once (count 3):\n%s", r[1].stdout)
	}
	t.Logf("%s (took %s)", strings.TrimSpace(r[1].stdout[strings.LastIndex(r[1].stdout, "count"):]), r[1].took)
}
