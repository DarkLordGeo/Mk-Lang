// Why: tls.Client must interoperate with a mature TLS 1.3 server and refuse
// what RFC 8446 says to refuse.
// What: handshakes over every suite, group and leaf key against Go's
// crypto/tls, ALPN, refusals, 1 MiB echoes, close_notify, stalls and malformed
// records from a raw server.
// How: Go's server runs on loopback in the test process with chains made by
// crypto/x509; each case checks the result and the alert Go receives.

package std

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tlsPeerDriver dials a server and runs one case, printing one line:
// "ok alpn=<p> n=<bytes echoed>", "err <the error>", or for the stall modes
// "closeerr <Close's error>".
const tlsPeerDriver = `import std.fs
import std.net
import std.time
import std.tls
import std.x509

// Stall is a transport over a stream whose 24-byte writes, the size of a
// protected alert record, never complete: a peer that stopped reading.
struct Stall {
    s: net.Conn
}

impl net.Transport for Stall {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        return try self.s.Read(io, buf)
    }

    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        if data.Len() == 24 {
            try io.Sleep(time.Hours(1))
        }
        try self.s.WriteAll(io, data)
    }

    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
        try self.s.Shutdown(io, how)
    }

    PeerAddr(&self): ?net.Addr {
        return none
    }

    Close(&self, io: Io): net.Error!void {
    }

    Abandon(self) {
        Stall(s) := self
        s.Abandon()
    }
}

Suites(a: &Arena, s: str): OutOfMemory![]u16 from a {
    var out := List[u16].New(a)
    if s == "-" {
        try out.Push(0x1301)
        try out.Push(0x1303)
        try out.Push(0x1302)
        return out.Done()[..]
    }
    for p in s.Split(",") {
        try out.Push(Trunc[u16](p.ParseInt() else { 0 }))
    }
    return out.Done()[..]
}

Names(a: &Arena, s: str into a): OutOfMemory![]str from a, s {
    var out := List[str].New(a)
    if s != "-" {
        for p in s.Split(",") {
            try out.Push(p)
        }
    }
    return out.Done()[..]
}

// SendAll writes data from a task of its own, beside the reads (one Read
// beside one WriteAll, as tls.Conn allows), recording a failure.
SendAll(c: &tls.Conn, io: Io, data: []u8, failed: mut bool) {
    c.WriteAll(io, data) else {
        *failed = true
    }
}

Echo(a: &Arena, io: Io, c: &tls.Conn, n: int): tls.Error|OutOfMemory!int {
    data := try a.Slice[u8](n)
    for i in 0..n {
        data[i] = Trunc[u8](i * 7 + 1)
    }
    back := try a.Slice[u8](n)
    var got := 0
    var failed := false
    scope s := io.Scope() {
        s.go SendAll(c, io, data[..], mut failed)
        for got < n {
            k := try c.Read(io, mut back[got..])
            if k == 0 {
                break
            }
            got += k
        }
    }
    if failed {
        return -2
    }
    for i in 0..got {
        if back[i] != data[i] {
            return -1
        }
    }
    return got
}

EchoNet(a: &Arena, io: Io, c: &net.Conn, n: int): net.Error|OutOfMemory!int {
    data := try a.Slice[u8](n)
    for i in 0..n {
        data[i] = Trunc[u8](i * 7 + 1)
    }
    try c.WriteAll(io, data[..])
    back := try a.Slice[u8](n)
    var got := 0
    for got < n {
        k := try c.Read(io, mut back[got..])
        if k == 0 {
            break
        }
        got += k
    }
    for i in 0..got {
        if back[i] != data[i] {
            return -1
        }
    }
    return got
}

// Shut checks a secured connection's Shutdown: of the read side, which
// sends nothing, after which writing still works; or of the write side
// after 5 bytes, which sends close_notify, after which the peer's answer
// is still read to its end. Returns the bytes written or read.
Shut(a: &Arena, io: Io, c: &net.Conn, read: bool): net.Error|OutOfMemory!int {
    data: [5]u8 = [1, 8, 15, 22, 29]
    if read {
        try c.Shutdown(io, net.Shutdown.Read)
        try c.WriteAll(io, data[..])
        return 5
    }
    try c.WriteAll(io, data[..])
    try c.Shutdown(io, net.Shutdown.Write)
    back := try a.Slice[u8](16)
    var got := 0
    for got < back.Len() {
        k := try c.Read(io, mut back[got..])
        if k == 0 {
            break
        }
        got += k
    }
    return got
}

// WakeCode is a net.Error as a small code for Wake's line: 0 none.
WakeCode(e: &net.Error): int {
    return match *e {
        Closed { 1 }
        net.Reset { 2 }
        time.TimedOut { 3 }
        Canceled { 4 }
        _ { 5 }
    }
}

const WakeNames: [6]str = ["ok", "Closed", "Reset", "TimedOut", "Canceled", "other"]

// ParkRead reads c once: the count, or -1 and the error's code.
ParkRead(io: Io, c: &net.Conn, k: mut int, code: mut int) {
    var b: [64]u8 = [0; 64]
    *k = c.Read(io, mut b[..]) else e {
        *code = WakeCode(&e)
        -1
    }
}

// ParkWrite writes data to c, which a peer that never reads stalls; the
// error's code.
ParkWrite(io: Io, c: &net.Conn, data: []u8, code: mut int) {
    c.WriteAll(io, data) else e {
        *code = WakeCode(&e)
    }
}

// Wake checks net.Transport rule (3) on a secured connection (TLS-R1,
// TLS-R2): a Read (read) or a 32 MiB WriteAll parked in a task, then,
// 300 ms later, Shutdown(Read) or Shutdown(Both) from this one. The line
// gives the parked call's answer, Shutdown's, and the milliseconds from
// Shutdown until the parked call returned; after Shutdown(Read) it also
// writes "hello", which the peer must read.
Wake(a: &Arena, io: Io, c: &net.Conn, read: bool): net.Error|OutOfMemory!str from a {
    big := try a.Slice[u8](if read { 1 } else { 32 << 20 })
    var k := -2
    var code := 0
    var shut := 0
    var start := time.Now(io)
    scope s := io.Scope() {
        if read {
            s.go ParkRead(io, c, mut k, mut code)
        } else {
            s.go ParkWrite(io, c, big[..], mut code)
        }
        io.Sleep(time.Millis(300)) else {}
        start = time.Now(io)
        how := if read { net.Shutdown.Read } else { net.Shutdown.Both }
        c.Shutdown(io, how) else e {
            shut = WakeCode(&e)
        }
    }
    ms := time.Now(io).Since(start).Nanos() / 1_000_000
    if read {
        hello: [5]u8 = [104, 101, 108, 108, 111]
        try c.WriteAll(io, hello[..])
        return try a.Fmt("ok wake read={k} code={c} shut={s} ms={ms}", (k, c: WakeNames[code], s: WakeNames[shut], ms))
    }
    return try a.Fmt("ok wake write={w} shut={s} ms={ms}", (w: WakeNames[code], s: WakeNames[shut], ms))
}

// SlowRead fills back with Reads that each wait at most 2 ms, counting
// the ones a deadline interrupted: the repeatable Read loses nothing,
// so the bytes match what one uninterrupted read would give.
SlowRead(io: Io, c: &tls.Conn, back: mut []u8, cancels: mut int): tls.Error!int {
    var got := 0
    for got < back.Len() {
        var k := 0
        var late := false
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(2))
            k = c.Read(io, mut back[got..]) else e {
                if !s.Expired() {
                    fail e
                }
                late = true
                0
            }
        }
        if late {
            *cancels += 1
        } else if k == 0 {
            break
        }
        got += k
    }
    return got
}

Run(sys: &System, a: &Arena): x509.Error|tls.Error|fs.Error!str from a *sys into a, *sys into sys {
    io := sys.io
    addr := sys.args[1]
    host := sys.args[2]
    roots := sys.args[3]
    mode := sys.args[6]
    n := Cast[int](sys.args[9].ParseInt() else { 5 })
    var cfg := tls.DefaultConfig
    hs := sys.args[7].ParseInt() else { 0 }
    if hs > 0 {
        cfg.handshake = time.Millis(hs)
    }
    cl := sys.args[8].ParseInt() else { 0 }
    if cl > 0 {
        cfg.close = time.Millis(cl)
    }
    cfg.alpn = try Names(a, sys.args[5])
    var text := ""
    if roots != "-" {
        text = try fs.ReadText(a, io, roots)
    }
    certs := try x509.ParsePem(a, text)
    all := certs.Done()
    pool := x509.Pool.New(all[..])
    if roots != "-" {
        cfg.roots = some(&pool)
    }
    suites := try Suites(a, sys.args[4])
    stream := try net.Dial(io, addr)
    var conn: net.Conn
    if mode.StartsWith("stall") {
        t := Stall(s: stream.Conn())
        conn = net.NewConn(sys.heap, t) else e {
            t.Abandon()
            return try a.Fmt("err {e}", (e))
        }
    } else {
        conn = stream.Conn()
    }
    if mode.StartsWith("connector") {
        sec := tls.Connector(&cfg)
        sc := sec.Connect(sys.heap, io, conn, host) else e {
            conn.Abandon()
            return try a.Fmt("err {e}", (e))
        }
        if mode == "connector-wakeread" || mode == "connector-wakewrite" {
            line := Wake(a, io, &sc, mode == "connector-wakeread") else e {
                sc.Abandon()
                return try a.Fmt("err {e}", (e))
            }
            sc.Close(io) else e {
                sc.Abandon()
                return try a.Fmt("closeerr {e}", (e))
            }
            return line
        }
        if mode != "connector" {
            shut := Shut(a, io, &sc, mode == "connector-shutread") else e {
                sc.Abandon()
                return try a.Fmt("err {e}", (e))
            }
            sc.Close(io) else e {
                sc.Abandon()
                return try a.Fmt("closeerr {e}", (e))
            }
            return try a.Fmt("ok shut n={shut}", (shut))
        }
        got := EchoNet(a, io, &sc, n) else e {
            sc.Abandon()
            return try a.Fmt("err {e}", (e))
        }
        sc.Close(io) else e {
            sc.Abandon()
            return try a.Fmt("closeerr {e}", (e))
        }
        return try a.Fmt("ok alpn=- n={got}", (got))
    }
    c := tls.FxClient(sys.heap, io, conn, &cfg, host, suites) else e {
        conn.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    onerr c.Abandon()
    if mode == "slowread" {
        back := try a.Slice[u8](n)
        var cancels := 0
        got := try SlowRead(io, &c, back, mut cancels)
        for i in 0..got {
            if back[i] != Trunc[u8](i * 7 + 1) {
                c.Abandon()
                return try a.Fmt("err byte {i} differs", (i))
            }
        }
        try c.Close(io)
        many := if cancels > 0 { "yes" } else { "no" }
        return try a.Fmt("ok n={got} interrupted={many}", (got, many))
    }
    p := try a.Dup(c.Alpn() else { "-" })
    got := Echo(a, io, &c, n) else e {
        c.Abandon()
        return try a.Fmt("err {e}", (e))
    }
    if mode == "stall" || mode == "stallnc" {
        q := if mode == "stall" { io } else { io.NoCancel() }
        start := time.Now(io)
        c.Close(q) else e {
            took := time.Now(io).Since(start).Nanos() / 1_000_000
            c.Abandon()
            return try a.Fmt("closeerr {e} ms={took}", (e, took))
        }
        return "closed"
    }
    c.Close(io) else e {
        c.Abandon()
        return try a.Fmt("closeerr {e}", (e))
    }
    return try a.Fmt("ok alpn={p} n={got}", (p, got))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    line := Run(sys, &arena) else e {
        try sys.Print("err {e}\n", (e))
        return
    }
    try sys.Print("{line}\n", (line))
}
`

var tlsPeerBuilt struct {
	sync.Mutex
	exe map[string]string
}

// tlsPeerExe builds the peer driver for mode once per test binary.
func tlsPeerExe(t *testing.T, mode string) string {
	t.Helper()
	tlsPeerBuilt.Lock()
	defer tlsPeerBuilt.Unlock()
	if tlsPeerBuilt.exe == nil {
		tlsPeerBuilt.exe = map[string]string{}
	}
	if exe, ok := tlsPeerBuilt.exe[mode]; ok {
		return exe
	}
	exe := tlsBuildDriver(t, "peer", mode, tlsPeerDriver)
	tlsPeerBuilt.exe[mode] = exe
	return exe
}

// tlsPKI is a root and the leaves the cases use.
type tlsPKI struct {
	rootPEM  string
	root     *x509.Certificate
	rootKey  crypto.Signer
	leaves   map[string]tls.Certificate
	otherPEM string
}

var (
	tlsPKIOnce sync.Once
	tlsPKIVal  *tlsPKI
	tlsPKIErr  error
)

func tlsMakeRoot(name string) (*x509.Certificate, crypto.Signer, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, nil, err
	}
	c, err := x509.ParseCertificate(der)
	return c, key, der, err
}

func tlsMakeLeaf(root *x509.Certificate, rootKey crypto.Signer, kind string, notAfter time.Time) (tls.Certificate, error) {
	var key crypto.Signer
	var err error
	switch kind {
	case "p256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "p384":
		key, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "ed25519":
		_, key, err = ed25519.GenerateKey(rand.Reader)
	case "rsa":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	}
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost", "example.test"},
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, key.Public(), rootKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// tlsPeerPKI makes the root, the other root and the leaves once.
func tlsPeerPKI(t *testing.T) *tlsPKI {
	t.Helper()
	tlsPKIOnce.Do(func() {
		p := &tlsPKI{leaves: map[string]tls.Certificate{}}
		var der []byte
		p.root, p.rootKey, der, tlsPKIErr = tlsMakeRoot("Mk TLS test root")
		if tlsPKIErr != nil {
			return
		}
		p.rootPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		_, _, other, err := tlsMakeRoot("Another root")
		if err != nil {
			tlsPKIErr = err
			return
		}
		p.otherPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other}))
		for _, k := range []string{"p256", "p384", "ed25519", "rsa"} {
			if p.leaves[k], tlsPKIErr = tlsMakeLeaf(p.root, p.rootKey, k, time.Now().Add(365*24*time.Hour)); tlsPKIErr != nil {
				return
			}
		}
		p.leaves["expired"], tlsPKIErr = tlsMakeLeaf(p.root, p.rootKey, "p256", time.Now().Add(-time.Hour))
		tlsPKIVal = p
	})
	if tlsPKIErr != nil {
		t.Fatal(tlsPKIErr)
	}
	return tlsPKIVal
}

// tlsServed is what Go's server saw of one connection.
type tlsServed struct {
	state tls.ConnectionState
	err   error
	eof   error
}

// tlsGoServer serves one connection with cfg, echoing until the client
// closes, and reports what it saw.
func tlsGoServer(t *testing.T, cfg *tls.Config) (string, <-chan tlsServed) {
	t.Helper()
	l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan tlsServed, 1)
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			done <- tlsServed{err: err}
			return
		}
		defer c.Close()
		tc := c.(*tls.Conn)
		tc.SetDeadline(time.Now().Add(60 * time.Second))
		if err := tc.Handshake(); err != nil {
			done <- tlsServed{err: err}
			return
		}
		buf := make([]byte, 65536)
		var rerr error
		for {
			n, err := tc.Read(buf)
			if n > 0 {
				if _, werr := tc.Write(buf[:n]); werr != nil {
					rerr = werr
					break
				}
			}
			if err != nil {
				rerr = err
				break
			}
		}
		done <- tlsServed{state: tc.ConnectionState(), eof: rerr}
	}()
	return l.Addr().String(), done
}

// tlsPeerCase is one run of the driver against Go's server.
type tlsPeerCase struct {
	name     string
	leaf     string
	roots    string // "root", "other" or "-"
	host     string
	suites   string
	alpn     string
	mode     string
	n        int
	curves   []tls.CurveID
	protos   []string
	want     string // the driver's line, or its prefix when it ends in "*"
	suite    uint16 // Go's ConnectionState.CipherSuite, when the handshake succeeds
	curve    tls.CurveID
	goErr    string // a substring of Go's handshake error
	closeEOF bool
}

func tlsRunPeer(t *testing.T, mode string, pki *tlsPKI, c tlsPeerCase) {
	t.Helper()
	cfg := &tls.Config{
		Certificates:     []tls.Certificate{pki.leaves[c.leaf]},
		MinVersion:       tls.VersionTLS13,
		CurvePreferences: c.curves,
		NextProtos:       c.protos,
	}
	addr, done := tlsGoServer(t, cfg)
	roots := "-"
	dir := t.TempDir()
	switch c.roots {
	case "root":
		roots = filepath.Join(dir, "root.pem")
		os.WriteFile(roots, []byte(pki.rootPEM), 0o644)
	case "other":
		roots = filepath.Join(dir, "other.pem")
		os.WriteFile(roots, []byte(pki.otherPEM), 0o644)
	}
	host := c.host
	if host == "" {
		host = "localhost"
	}
	if host == "-" {
		host = ""
	}
	suites, alpn, m := c.suites, c.alpn, c.mode
	if suites == "" {
		suites = "-"
	}
	if alpn == "" {
		alpn = "-"
	}
	if m == "" {
		m = "plain"
	}
	n := c.n
	if n == 0 {
		n = 5
	}
	r := runNative(t, tlsPeerExe(t, mode), "", addr, host, roots, suites, alpn, m, "0", "300", fmt.Sprint(n))
	got := strings.TrimSpace(r.stdout)
	if r.code != 0 {
		t.Fatalf("driver exit %d: %s %s", r.code, got, r.stderr)
	}
	if strings.HasSuffix(c.want, "*") {
		if !strings.HasPrefix(got, strings.TrimSuffix(c.want, "*")) {
			t.Errorf("got %q, want %q", got, c.want)
		}
	} else if got != c.want {
		t.Errorf("got %q, want %q", got, c.want)
	}
	select {
	case s := <-done:
		if c.goErr != "" {
			if s.err == nil || !strings.Contains(s.err.Error(), c.goErr) {
				t.Errorf("Go's server: %v, want an error with %q", s.err, c.goErr)
			}
			return
		}
		if s.err != nil {
			t.Errorf("Go's server: %v", s.err)
			return
		}
		if s.state.Version != tls.VersionTLS13 {
			t.Errorf("version %x", s.state.Version)
		}
		if c.suite != 0 && s.state.CipherSuite != c.suite {
			t.Errorf("suite %x, want %x", s.state.CipherSuite, c.suite)
		}
		if c.curve != 0 && s.state.CurveID != c.curve {
			t.Errorf("curve %v, want %v", s.state.CurveID, c.curve)
		}
		if c.closeEOF && s.eof != io.EOF {
			t.Errorf("Go's server read %v after the client's Close, want io.EOF (close_notify)", s.eof)
		}
	case <-time.After(30 * time.Second):
		t.Errorf("Go's server did not finish")
	}
}

// TestStdTls_GoPeerHandshakes runs every case against Go's server in dev
// and release.
func TestStdTls_GoPeerHandshakes(t *testing.T) {
	pki := tlsPeerPKI(t)
	cases := []tlsPeerCase{
		{name: "aes128gcm", leaf: "p256", roots: "root", suites: "4865", want: "ok alpn=- n=5", suite: tls.TLS_AES_128_GCM_SHA256, curve: tls.X25519, closeEOF: true},
		{name: "aes256gcm", leaf: "p256", roots: "root", suites: "4866", want: "ok alpn=- n=5", suite: tls.TLS_AES_256_GCM_SHA384, curve: tls.X25519, closeEOF: true},
		{name: "chacha20poly1305", leaf: "p256", roots: "root", suites: "4867", want: "ok alpn=- n=5", suite: tls.TLS_CHACHA20_POLY1305_SHA256, curve: tls.X25519, closeEOF: true},
		{name: "p256 through HelloRetryRequest", leaf: "p256", roots: "root", curves: []tls.CurveID{tls.CurveP256}, want: "ok alpn=- n=5", curve: tls.CurveP256, closeEOF: true},
		{name: "p256 HRR aes256", leaf: "p256", roots: "root", suites: "4866", curves: []tls.CurveID{tls.CurveP256}, want: "ok alpn=- n=5", suite: tls.TLS_AES_256_GCM_SHA384, curve: tls.CurveP256},
		{name: "leaf p384", leaf: "p384", roots: "root", want: "ok alpn=- n=5"},
		{name: "leaf ed25519", leaf: "ed25519", roots: "root", want: "ok alpn=- n=5"},
		{name: "leaf rsa-pss", leaf: "rsa", roots: "root", want: "ok alpn=- n=5"},
		{name: "alpn one", leaf: "p256", roots: "root", alpn: "http/1.1", protos: []string{"h2", "http/1.1"}, want: "ok alpn=http/1.1 n=5"},
		{name: "alpn server preference", leaf: "p256", roots: "root", alpn: "http/1.1,h2", protos: []string{"h2", "http/1.1"}, want: "ok alpn=h2 n=5"},
		{name: "alpn no overlap", leaf: "p256", roots: "root", alpn: "x", protos: []string{"h2"}, want: "err Alert(code: 120)", goErr: "unsupported application protocols"},
		{name: "unknown authority", leaf: "p256", roots: "other", want: "err UnknownAuthority", goErr: "unknown certificate authority"},
		{name: "name mismatch", leaf: "p256", roots: "root", host: "other.test", want: "err NameMismatch", goErr: "bad certificate"},
		{name: "expired", leaf: "expired", roots: "root", want: "err Expired", goErr: "expired certificate"},
		// roots: none, DefaultConfig's own route: x509.SystemRoots names no
		// certificate on Windows, so Verify asks the platform, which does
		// not trust the test root.
		{name: "system roots refuse the test root", leaf: "p256", roots: "none", want: "err UnknownAuthority", goErr: "unknown certificate authority"},
		{name: "empty host refused before any byte", leaf: "p256", roots: "root", host: "-", want: "err Unsupported", goErr: "EOF"},
		{name: "one MiB both ways", leaf: "p256", roots: "root", n: 1 << 20, want: "ok alpn=- n=1048576", closeEOF: true},
		{name: "connector", leaf: "p256", roots: "root", mode: "connector", want: "ok alpn=- n=5", closeEOF: true},
		{name: "connector unknown authority", leaf: "p256", roots: "other", mode: "connector", want: "err SecureFailed(why: UnknownAuthority)", goErr: "unknown certificate authority"},
		{name: "close past its limit", leaf: "p256", roots: "root", mode: "stall", want: "closeerr TimedOut ms=*"},
		{name: "close past its limit under NoCancel", leaf: "p256", roots: "root", mode: "stallnc", want: "closeerr TimedOut ms=*"},
	}
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) { tlsRunPeer(t, mode, pki, c) })
		}
	}
}

// tlsRawServer accepts one connection, reads the client's first flight,
// writes reply, then reads what the client sends back for up to a second.
func tlsRawServer(t *testing.T, reply []byte) (string, <-chan []byte) {
	return tlsRawServerFn(t, reply, nil)
}

// tlsRawServerFn is tlsRawServer whose reply, when fn is not nil, is
// fn of the ClientHello record's body.
func tlsRawServerFn(t *testing.T, reply []byte, fn func(hello []byte) []byte) (string, <-chan []byte) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan []byte, 1)
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			out <- nil
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(20 * time.Second))
		head := make([]byte, 5)
		if _, err := io.ReadFull(c, head); err != nil {
			out <- nil
			return
		}
		body := make([]byte, int(head[3])<<8|int(head[4]))
		io.ReadFull(c, body)
		if fn != nil {
			reply = fn(body)
		}
		if reply == nil {
			// A peer that stalls: read nothing more, write nothing.
			time.Sleep(15 * time.Second)
			out <- nil
			return
		}
		c.Write(reply)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		back, _ := io.ReadAll(c)
		out <- back
	}()
	return l.Addr().String(), out
}

// tlsAlertRecord is a plaintext fatal alert record.
func tlsAlertRecord(code byte) []byte {
	return []byte{21, 3, 3, 0, 2, 2, code}
}

// tlsServerHello12 is a TLS 1.2 ServerHello with no extensions.
func tlsServerHello12() []byte {
	body := []byte{3, 3}
	body = append(body, bytes.Repeat([]byte{7}, 32)...)
	body = append(body, 0, 0xc0, 0x2f, 0)
	msg := append([]byte{2, 0, 0, byte(len(body))}, body...)
	return append([]byte{22, 3, 3, 0, byte(len(msg))}, msg...)
}

// tlsServerHello13 is a TLS 1.3 ServerHello choosing suite, echoing the
// session id of hello (a ClientHello handshake message) and sharing a
// fixed X25519 key: well formed, so the client's check of the suite is
// what refuses it.
func tlsServerHello13(suite uint16) func(hello []byte) []byte {
	return func(hello []byte) []byte {
		if len(hello) < 4+2+32+1 {
			return nil
		}
		n := int(hello[38])
		if len(hello) < 39+n {
			return nil
		}
		body := []byte{3, 3}
		body = append(body, bytes.Repeat([]byte{7}, 32)...)
		body = append(body, byte(n))
		body = append(body, hello[39:39+n]...)
		body = append(body, byte(suite>>8), byte(suite), 0)
		ext := []byte{0, 43, 0, 2, 3, 4, 0, 51, 0, 36, 0, 29, 0, 32}
		ext = append(ext, bytes.Repeat([]byte{9}, 32)...)
		body = append(body, byte(len(ext)>>8), byte(len(ext)))
		body = append(body, ext...)
		msg := append([]byte{2, 0, 0, byte(len(body))}, body...)
		return append([]byte{22, 3, 3, 0, byte(len(msg))}, msg...)
	}
}

// TestStdTls_GoPeerMalformed checks what the client sends and reports for
// a raw server's malformed or refusing answers, and that a stalled server
// makes Client return time.TimedOut.
func TestStdTls_GoPeerMalformed(t *testing.T) {
	pki := tlsPeerPKI(t)
	_ = pki
	overflow := append([]byte{22, 3, 3, 0x40, 0x01}, make([]byte, 16385)...)
	cases := []struct {
		name   string
		reply  []byte
		want   string
		alert  []byte // what the client must send back; nil for nothing
		hs     string
		fn     func([]byte) []byte
		suites string
	}{
		{"garbage handshake", []byte{22, 3, 3, 0, 5, 'h', 'e', 'l', 'l', 'o'}, "err Protocol", tlsAlertRecord(50), "0", nil, ""},
		{"unknown record type", []byte{99, 3, 3, 0, 1, 0}, "err Protocol", tlsAlertRecord(10), "0", nil, ""},
		{"record overflow", overflow, "err Protocol", tlsAlertRecord(22), "0", nil, ""},
		{"server alert", tlsAlertRecord(40), "err Alert(code: 40)", []byte{}, "0", nil, ""},
		{"tls 1.2 server", tlsServerHello12(), "err Unsupported", tlsAlertRecord(70), "0", nil, ""},
		{"stall under 300 ms", nil, "err TimedOut", nil, "300", nil, ""},
		{"suite not offered", nil, "err Protocol", tlsAlertRecord(47), "0", tlsServerHello13(0x1302), "4865"},
		{"suite unknown", nil, "err Protocol", tlsAlertRecord(47), "0", tlsServerHello13(0x1304), "-"},
	}
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				addr, back := tlsRawServerFn(t, c.reply, c.fn)
				suites := c.suites
				if suites == "" {
					suites = "-"
				}
				start := time.Now()
				r := runNative(t, tlsPeerExe(t, mode), "", addr, "localhost", "-", suites, "-", "plain", c.hs, "300", "5")
				took := time.Since(start)
				got := strings.TrimSpace(r.stdout)
				if got != c.want {
					t.Fatalf("got %q (%s), want %q", got, r.stderr, c.want)
				}
				if c.hs == "300" && took > 5*time.Second {
					t.Errorf("a 300 ms handshake limit took %v", took)
				}
				if c.alert == nil {
					return
				}
				b := <-back
				if len(c.alert) == 0 {
					if len(b) != 0 {
						t.Errorf("the client answered an alert with %x", b)
					}
					return
				}
				if !bytes.HasPrefix(b, c.alert) {
					t.Errorf("the client sent %x, want the alert %x", b, c.alert)
				}
			})
		}
	}
}

// tlsSlowProxy forwards a client to target, the server's bytes in pieces
// of 97 bytes a millisecond apart, so the client's records arrive in many
// reads.
func tlsSlowProxy(t *testing.T, target string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		u, err := net.Dial("tcp", target)
		if err != nil {
			return
		}
		defer u.Close()
		go io.Copy(u, c)
		buf := make([]byte, 97)
		for {
			n, err := u.Read(buf)
			if n > 0 {
				if _, err := c.Write(buf[:n]); err != nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
			if err != nil {
				return
			}
		}
	}()
	return l.Addr().String()
}

// TestStdTls_GoPeerReadRepeatable checks that Read is repeatable: 64 KiB
// from Go's server reach the client in 97-byte pieces while every Read
// waits at most 2 ms; the interrupted Reads lose nothing, and the bytes
// are the server's, in order.
// It is a cancel-safety test; tools/gate/safety.go lists it.
func TestStdTls_GoPeerReadRepeatable(t *testing.T) {
	pki := tlsPeerPKI(t)
	const n = 65536
	for _, mode := range tlsModes {
		t.Run(mode, func(t *testing.T) {
			cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}, MinVersion: tls.VersionTLS13}
			l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			go func() {
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				data := make([]byte, n)
				for i := range data {
					data[i] = byte(i*7 + 1)
				}
				c.Write(data)
				io.Copy(io.Discard, c)
			}()
			roots := filepath.Join(t.TempDir(), "root.pem")
			os.WriteFile(roots, []byte(pki.rootPEM), 0o644)
			addr := tlsSlowProxy(t, l.Addr().String())
			r := runNative(t, tlsPeerExe(t, mode), "", addr, "localhost", roots, "-", "-", "slowread", "0", "0", fmt.Sprint(n))
			if got := strings.TrimSpace(r.stdout); got != "ok n=65536 interrupted=yes" {
				t.Fatalf("got %q (%s)", got, r.stderr)
			}
		})
	}
}

// TestStdTls_StalledPeerDefaultLimit checks that a peer that stalls
// mid-handshake makes Client return time.TimedOut under DefaultConfig's
// 10-second limit, and not much later.
func TestStdTls_StalledPeerDefaultLimit(t *testing.T) {
	addr, _ := tlsRawServer(t, nil)
	start := time.Now()
	r := runNative(t, tlsPeerExe(t, "release"), "", addr, "localhost", "-", "-", "-", "plain", "0", "0", "5")
	took := time.Since(start)
	if got := strings.TrimSpace(r.stdout); got != "err TimedOut" {
		t.Fatalf("got %q (%s)", got, r.stderr)
	}
	if took < 9*time.Second || took > 14*time.Second {
		t.Errorf("DefaultConfig's 10 s handshake limit fired after %v", took)
	}
}

// tlsOneByteRun serves 4096 bytes as 4096 one-byte records (one Write
// each) from Go's server with cfg and has exe read them with Reads that
// each wait at most 2 ms: TCP delivers many records in one read, so the
// read-ahead keeps its start offset across records and moves a partial
// record only at the buffer's end; the bytes arrive whole and in
// order.
func tlsOneByteRun(t *testing.T, pki *tlsPKI, exe string, cfg *tls.Config) {
	t.Helper()
	const n = 4096
	l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; i < n; i++ {
			if _, err := c.Write([]byte{byte(i*7 + 1)}); err != nil {
				return
			}
		}
		io.Copy(io.Discard, c)
	}()
	roots := filepath.Join(t.TempDir(), "root.pem")
	os.WriteFile(roots, []byte(pki.rootPEM), 0o644)
	r := runNative(t, exe, "", l.Addr().String(), "localhost", roots, "-", "-", "slowread", "0", "0", fmt.Sprint(n))
	if got := strings.TrimSpace(r.stdout); !strings.HasPrefix(got, "ok n=4096 interrupted=") {
		t.Fatalf("got %q (%s)", got, r.stderr)
	}
}

// TestStdTls_GoPeerOneByteRecords runs tlsOneByteRun against TLS 1.3.
func TestStdTls_GoPeerOneByteRecords(t *testing.T) {
	pki := tlsPeerPKI(t)
	for _, mode := range tlsModes {
		t.Run(mode, func(t *testing.T) {
			cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}, MinVersion: tls.VersionTLS13}
			tlsOneByteRun(t, pki, tlsPeerExe(t, mode), cfg)
		})
	}
}

// TestStdTls_GoPeerSecuredShutdown checks tls.Connector's transport's
// Shutdown against Go's server: Shutdown(Read) sends nothing (Go's server
// reads the 5 bytes written after it, then the close_notify of Close), and
// Shutdown(Write) sends close_notify after the 5 bytes (Go reads io.EOF),
// after which the client still reads Go's 3-byte answer to its end.
func TestStdTls_GoPeerSecuredShutdown(t *testing.T) {
	pki := tlsPeerPKI(t)
	for _, mode := range tlsModes {
		for _, how := range []string{"shutread", "shutwrite"} {
			t.Run(mode+"/"+how, func(t *testing.T) {
				cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}, MinVersion: tls.VersionTLS13}
				l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Close()
				type seen struct {
					got []byte
					err error
				}
				done := make(chan seen, 1)
				go func() {
					c, err := l.Accept()
					if err != nil {
						done <- seen{err: err}
						return
					}
					defer c.Close()
					c.SetDeadline(time.Now().Add(20 * time.Second))
					got, err := io.ReadAll(c)
					if err == nil && how == "shutwrite" {
						_, err = c.Write([]byte("bye"))
					}
					done <- seen{got: got, err: err}
				}()
				roots := filepath.Join(t.TempDir(), "root.pem")
				os.WriteFile(roots, []byte(pki.rootPEM), 0o644)
				r := runNative(t, tlsPeerExe(t, mode), "", l.Addr().String(), "localhost", roots, "-", "-", "connector-"+how, "0", "0", "5")
				want := "ok shut n=5"
				if how == "shutwrite" {
					want = "ok shut n=3"
				}
				if got := strings.TrimSpace(r.stdout); got != want {
					t.Errorf("got %q (%s), want %q", got, r.stderr, want)
				}
				s := <-done
				if s.err != nil || !bytes.Equal(s.got, []byte{1, 8, 15, 22, 29}) {
					t.Errorf("Go's server read %x, %v; want 01080f161d and the end of the stream (close_notify)", s.got, s.err)
				}
			})
		}
	}
}
