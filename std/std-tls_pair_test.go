// Why: the server role and the post-handshake paths (KeyUpdate, renegotiation,
// a failed connection) need tests with std.tls on both ends.
// What: TestStdTls_PairCases: every suite, KeyUpdate both ways, refused
// renegotiation, a failed connection after an interrupted WriteAll, and
// tls.Connector against tls.Acceptor through net.Secure.
// How: tls.Client and tls.Server run as two tasks of one Mk program over
// loopback.
// Its tests are cancel-safety tests; tools/gate/safety.go lists each.

package std

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tlsPairDriver runs one case: its arguments are the server's chain and
// key PEM files, the root's PEM file, the case and the client's suite.
const tlsPairDriver = `import std.crypto
import std.encoding
import std.fs
import std.net
import std.time
import std.tls
import std.x509

// Code names an error for the server's report.
Code(e: &tls.Error): int {
    return match *e {
        tls.Protocol { 100 }
        tls.Alert(code) { 200 + int(code) }
        Canceled { 300 }
        net.Reset { 400 }
        _ { 999 }
    }
}

ReadFull(io: Io, c: &tls.Conn, buf: mut []u8): tls.Error!bool {
    var got := 0
    for got < buf.Len() {
        k := try c.Read(io, mut buf[got..])
        if k == 0 {
            return false
        }
        got += k
    }
    return true
}

UntilEnd(io: Io, c: &tls.Conn): tls.Error!void {
    var buf: [64]u8 = [0; 64]
    for {
        if try c.Read(io, mut buf[..]) == 0 {
            return
        }
    }
}

// The stress case (L1): StressWriters tasks each send StressMsgs
// messages of 64 bytes with WriteAll on one Conn while two tasks Read it;
// the server echoes StressTotal bytes back.
const StressWriters: int = 8
const StressMsgs: int = 200
const StressTotal: int = 102400

StressByte(w: int, m: int, j: int): u8 {
    return Trunc[u8](w * 31 + m * 7 + j)
}

Writer(c: &tls.Conn, io: Io, w: int, fails: &Mutex[int]) {
    var msg: [64]u8 = [0; 64]
    for m in 0..StressMsgs {
        for j in 0..64 {
            msg[j] = StressByte(w, m, j)
        }
        c.WriteAll(io, msg[..]) else {
            var g := fails.Lock(io) else {
                return
            }
            *g.Mut() += 1
            g.Unlock()
            return
        }
    }
}

Reader(c: &tls.Conn, io: Io, size: int, total: &Mutex[int], sum: &Mutex[int]) {
    var buf: [1000]u8 = [0; 1000]
    var n := 0
    var t := 0
    for {
        k := c.Read(io, mut buf[0..size]) else {
            n = -1000000
            break
        }
        if k == 0 {
            break
        }
        n += k
        for i in 0..k {
            t += int(buf[i])
        }
    }
    var g := total.Lock(io) else {
        return
    }
    *g.Mut() += n
    g.Unlock()
    var g2 := sum.Lock(io) else {
        return
    }
    *g2.Mut() += t
    g2.Unlock()
}

// WokenReader reads once and stores what the Read returned: the error's
// Code, 1 for the end, 2 for data.
WokenReader(c: &tls.Conn, io: Io, code: &Mutex[int]) {
    var buf: [4]u8 = [0; 4]
    var v := 2
    k := c.Read(io, mut buf[..]) else e {
        v = Code(&e)
        0
    }
    if v == 2 && k == 0 {
        v = 1
    }
    var g := code.Lock(io) else {
        return
    }
    *g.Mut() = v
    g.Unlock()
}

// Answer is the server's side of a case after the handshake.
Answer(io: Io, c: &tls.Conn, mode: int): tls.Error!void {
    var buf: [4]u8 = [0; 4]
    if mode == 10 {
        var big: [4096]u8 = [0; 4096]
        var got := 0
        for got < StressTotal {
            k := try c.Read(io, mut big[..])
            if k == 0 {
                fail tls.Unsupported
            }
            try c.WriteAll(io, big[0..k])
            got += k
        }
        // close_notify while the transport stays open, so the client's
        // readers end and its Close reaches this side.
        try tls.FxNotify(c, io)
        try UntilEnd(io, c)
        return
    }
    if mode == 2 || mode == 5 {
        try UntilEnd(io, c)
        return
    }
    if mode == 3 || mode == 11 {
        UntilEnd(io, c) else {}
        return
    }
    if mode == 9 {
        // Both keys near their record limit (RFC 8446 5.5), as the
        // client set its own: each direction's KeyUpdate comes from
        // WriteAll itself.
        try tls.FxWear(c, io, 2)
        try tls.FxWearRead(c, io, 2)
    }
    if mode == 7 || mode == 8 {
        // Records that deliver no data, empty application data and
        // KeyUpdates in turn: 16 in a row pass, the 17th is refused.
        if !try ReadFull(io, c, mut buf[..]) {
            return
        }
        count := if mode == 7 { 16 } else { 17 }
        for i in 0..count {
            if i % 2 == 0 {
                try tls.FxSendRaw(c, io, 23, buf[0..0])
            } else {
                try tls.FxRequestUpdate(c, io)
            }
        }
        try c.WriteAll(io, "pong".AsBytes())
        try UntilEnd(io, c)
        return
    }
    var rounds := if mode == 1 || mode == 9 { 3 } else { 1 }
    for rounds > 0 {
        if !try ReadFull(io, c, mut buf[..]) {
            return
        }
        try c.WriteAll(io, buf[..])
        rounds -= 1
    }
    if mode == 1 {
        try tls.FxRequestUpdate(c, io)
        try c.WriteAll(io, "srv!".AsBytes())
    }
    if mode == 9 && try tls.FxWriteSeq(c, io) > 1000 {
        fail tls.Unsupported
    }
    try UntilEnd(io, c)
}

ServerSide(h: &Heap, io: Io, l: &net.Listener, cfg: &tls.Config, mode: int, code: mut int) {
    s := l.Accept(io) else {
        *code = 1
        return
    }
    conn := s.Conn()
    if mode == 4 || mode == 6 {
        sec := tls.Acceptor(cfg)
        nc := sec.Connect(h, io, conn, "") else {
            conn.Abandon()
            *code = 2
            return
        }
        var buf: [4]u8 = [0; 4]
        var got := 0
        for got < 4 {
            k := nc.Read(io, mut buf[got..]) else {
                *code = 3
                break
            }
            if k == 0 {
                break
            }
            got += k
        }
        if mode == 6 {
            // The client shut its write side: close_notify, read as the end.
            k := nc.Read(io, mut buf[0..1]) else {
                *code = 5
                0
            }
            if k != 0 {
                *code = 6
            }
        }
        nc.WriteAll(io, buf[..]) else {}
        nc.Close(io) else {
            nc.Abandon()
            return
        }
        return
    }
    c := tls.Server(h, io, conn, cfg) else e {
        conn.Abandon()
        *code = Code(&e)
        return
    }
    Answer(io, &c, mode) else e {
        *code = Code(&e)
        c.Abandon()
        return
    }
    c.Close(io) else e {
        *code = Code(&e)
        c.Abandon()
        return
    }
}

Same(b: []u8, s: str): bool {
    t := str.From(b) else {
        return false
    }
    return t == s
}

// Ask is the client's side of a case after the handshake; it returns
// what it saw.
Ask(a: &Arena, io: Io, c: &tls.Conn, mode: int): tls.Error|OutOfMemory!str from a {
    var buf: [4]u8 = [0; 4]
    if mode == 2 || mode == 5 {
        if mode == 2 {
            renegotiate: [8]u8 = [1, 0, 0, 4, 3, 3, 0, 0]
            try tls.FxSendRaw(c, io, 22, renegotiate[..])
        } else {
            forged: [7]u8 = [21, 3, 3, 0, 2, 1, 0]
            try tls.FxWriteRawConn(c, io, forged[..])
        }
        _ = c.Read(io, mut buf[..]) else e {
            return try a.Fmt("read {e}", (e))
        }
        return "read ok"
    }
    if mode == 10 {
        total := Mutex[int].New(0)
        sum := Mutex[int].New(0)
        fails := Mutex[int].New(0)
        scope s := io.Scope() {
            s.go Reader(c, io, 13, &total, &sum)
            s.go Reader(c, io, 1000, &total, &sum)
            for w in 0..StressWriters {
                s.go Writer(c, io, w, &fails)
            }
        }
        var want := 0
        for w in 0..StressWriters {
            for m in 0..StressMsgs {
                for j in 0..64 {
                    want += int(StressByte(w, m, j))
                }
            }
        }
        t := total.Into()
        su := sum.Into()
        f := fails.Into()
        sums := if su == want { "sum ok" } else { "sum differs" }
        return try a.Fmt("stress n={t} {sums} fails={f}", (t, sums, f))
    }
    if mode == 11 {
        // L4: a Read blocked in one task returns the failure at once when
        // another task's WriteAll is interrupted.
        code := Mutex[int].New(0)
        scope s := io.Scope() {
            s.go WokenReader(c, io, &code)
            io.Sleep(time.Millis(50)) else {}
            scope w := io.Scope() {
                w.CancelAfter(time.Nanos(0))
                c.WriteAll(io, "lost".AsBytes()) else {}
            }
        }
        v := code.Into()
        return try a.Fmt("woken {v}", (v))
    }
    if mode == 7 || mode == 8 {
        try c.WriteAll(io, "ping".AsBytes())
        got := ReadFull(io, c, mut buf[..]) else e {
            return try a.Fmt("read {e}", (e))
        }
        return if got && Same(buf[..], "pong") { "got pong" } else { "pong lost" }
    }
    if mode == 3 {
        scope s := io.Scope() {
            s.CancelAfter(time.Nanos(0))
            c.WriteAll(io, "lost".AsBytes()) else e {
                _ = e
            }
        }
        r := c.Read(io, mut buf[..]) else e {
            w := c.WriteAll(io, "more".AsBytes()) else e2 {
                return try a.Fmt("read {e} write {e2}", (e, e2))
            }
            _ = w
            return "write ok"
        }
        _ = r
        return "read ok"
    }
    if mode == 9 {
        try tls.FxWear(c, io, 2)
        try tls.FxWearRead(c, io, 2)
    }
    var rounds := if mode == 1 || mode == 9 { 3 } else { 1 }
    for rounds > 0 {
        if mode == 1 {
            try tls.FxRequestUpdate(c, io)
        }
        try c.WriteAll(io, "ping".AsBytes())
        if !try ReadFull(io, c, mut buf[..]) || !Same(buf[..], "ping") {
            return "echo lost"
        }
        rounds -= 1
    }
    if mode == 1 {
        if !try ReadFull(io, c, mut buf[..]) || !Same(buf[..], "srv!") {
            return "srv lost"
        }
    }
    if mode == 9 && try tls.FxWriteSeq(c, io) > 1000 {
        return "not rekeyed"
    }
    return "echoed"
}

Pair(sys: &System, a: &Arena into a): x509.Error|tls.Error|fs.Error|encoding.Error|crypto.Error|OutOfMemory!str from a {
    io := sys.io
    certs := try x509.ParsePem(a, try fs.ReadText(a, io, sys.args[1]))
    blocks := try encoding.ParsePem(a, try fs.ReadText(a, io, sys.args[2]))
    key := try crypto.ParsePrivateKey(blocks[0].der)
    roots := try x509.ParsePem(a, try fs.ReadText(a, io, sys.args[3]))
    mode := Cast[int](sys.args[4].ParseInt() else { 0 })
    suite: [1]u16 = [Trunc[u16](sys.args[5].ParseInt() else { 0x1301 })]
    all := certs.Done()
    ids: [1]tls.Identity = [tls.Identity(chain: all[..], key: &key)]
    var scfg := tls.DefaultConfig
    scfg.identities = ids[..]
    rs := roots.Done()
    pool := x509.Pool.New(rs[..])
    var ccfg := tls.DefaultConfig
    ccfg.roots = some(&pool)
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var b: [32]u8 = [0; 32]
    addr := try a.Fmt("127.0.0.1:{p}", (p: l.Port()))
    var code := 0
    var said := ""
    scope s := io.Scope() {
        s.go ServerSide(sys.heap, io, &l, &scfg, mode, mut code)
        stream := try net.Dial(io, addr)
        conn := stream.Conn()
        if mode == 4 || mode == 6 {
            sec := tls.Connector(&ccfg)
            nc := sec.Connect(sys.heap, io, conn, "localhost") else e {
                conn.Abandon()
                fail e
            }
            onerr nc.Abandon()
            try nc.WriteAll(io, "ping".AsBytes())
            var half := ""
            if mode == 6 {
                // A half-close: close_notify and the transport's write
                // side; writing is Closed, reading goes on.
                if nc.PeerAddr().IsNone() {
                    half = "no peer address; "
                }
                try nc.Shutdown(io, net.Shutdown.Write)
                nc.WriteAll(io, "more".AsBytes()) else e {
                    half = try a.Fmt("{half}write after shutdown {e}; ", (half, e))
                }
            }
            var got := 0
            for got < 4 {
                k := try nc.Read(io, mut b[got..4])
                if k == 0 {
                    break
                }
                got += k
            }
            echoed := if Same(b[0..4], "ping") { "echoed" } else { "echo lost" }
            said = try a.Fmt("{half}{echoed}", (half, echoed))
            try nc.Close(io)
        } else {
            c := tls.FxClient(sys.heap, io, conn, &ccfg, "localhost", suite[..]) else e {
                conn.Abandon()
                fail e
            }
            said = Ask(a, io, &c, mode) else e {
                c.Abandon()
                fail e
            }
            if mode == 2 || mode == 3 || mode == 5 || mode == 8 || mode == 11 {
                c.Close(io) else e {
                    c.Abandon()
                    said = try a.Fmt("{said} close {e}", (said, e))
                }
            } else {
                c.Close(io) else e {
                    c.Abandon()
                    fail e
                }
            }
        }
    }
    return try a.Fmt("{said} server={code}", (said, code))
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    line := Pair(sys, &arena) else e {
        try sys.Print("err {e}\n", (e))
        return
    }
    try sys.Print("{line}\n", (line))
}
`

var tlsPairBuilt struct {
	sync.Mutex
	exe map[string]string
}

func tlsPairExe(t *testing.T, mode string) string {
	t.Helper()
	tlsPairBuilt.Lock()
	defer tlsPairBuilt.Unlock()
	if tlsPairBuilt.exe == nil {
		tlsPairBuilt.exe = map[string]string{}
	}
	if exe, ok := tlsPairBuilt.exe[mode]; ok {
		return exe
	}
	exe := tlsBuildDriver(t, "pair", mode, tlsPairDriver)
	tlsPairBuilt.exe[mode] = exe
	return exe
}

// tlsLeafFiles writes the leaf's chain, its key (PKCS #8) and the root as
// PEM files into dir and returns their paths.
func tlsLeafFiles(t *testing.T, dir string, pki *tlsPKI, leaf string) (chain, key, root string) {
	t.Helper()
	l := pki.leaves[leaf]
	chain = filepath.Join(dir, leaf+"-chain.pem")
	key = filepath.Join(dir, leaf+"-key.pem")
	root = filepath.Join(dir, "root.pem")
	der, err := x509.MarshalPKCS8PrivateKey(l.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for p, b := range map[string][]byte{
		chain: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: l.Certificate[0]}),
		key:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
		root:  []byte(pki.rootPEM),
	} {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return chain, key, root
}

// tlsLineMatches reports whether a driver's line is want, or starts with
// want's text before a final "*".
func tlsLineMatches(got, want string) bool {
	if p, ok := strings.CutSuffix(want, "*"); ok {
		return strings.HasPrefix(got, p)
	}
	return got == want
}

// tlsPairCase is one Mk-to-Mk case: the server's leaf, the driver's
// mode, the client's suite and the line it must print (a prefix when it
// ends in "*").
type tlsPairCase struct {
	name, leaf string
	mode       int
	suite      int
	want       string
}

// tlsPairTable is every Mk-to-Mk case, run on both targets.
var tlsPairTable = []tlsPairCase{
	{"aes128gcm", "p256", 0, 0x1301, "echoed server=0"},
	{"aes256gcm", "p256", 0, 0x1302, "echoed server=0"},
	{"chacha20poly1305", "ed25519", 0, 0x1303, "echoed server=0"},
	{"key updates both ways", "p256", 1, 0x1301, "echoed server=0"},
	{"key updates both ways aes256", "ed25519", 1, 0x1302, "echoed server=0"},
	{"renegotiation refused", "p256", 2, 0x1301, "read Alert(code: 10) close Alert(code: 10) server=100"},
	{"interrupted write fails the connection", "p256", 3, 0x1303, "read Canceled write Canceled close Canceled server=*"},
	{"forged plaintext close_notify refused", "p256", 5, 0x1301, "read Alert(code: 10) close Alert(code: 10) server=100"},
	{"connector and acceptor", "ed25519", 4, 0x1301, "echoed server=0"},
	{"keys renewed at the record limit aes128gcm", "p256", 9, 0x1301, "echoed server=0"},
	{"keys renewed at the record limit aes256gcm", "ed25519", 9, 0x1302, "echoed server=0"},
	{"keys renewed before the sequence wraps chacha20poly1305", "p256", 9, 0x1303, "echoed server=0"},
	{"half-close keeps reading", "p256", 6, 0x1301, "write after shutdown Closed; echoed server=0"},
	{"sixteen records without data pass", "p256", 7, 0x1301, "got pong server=0"},
	{"seventeen records without data refused", "ed25519", 8, 0x1303, "read Protocol close Protocol server=*"},
	{"eight writers and two readers on one conn", "p256", 10, 0x1303, "stress n=102400 sum ok fails=0 server=0"},
	{"a blocked read woken by the failed write", "p256", 11, 0x1301, "woken 300 close Canceled server=*"},
}

// TestStdTls_PairCases runs every Mk-to-Mk case in dev and release, with
// a P-256 and an Ed25519 server key.
func TestStdTls_PairCases(t *testing.T) {
	pki := tlsPeerPKI(t)
	cases := tlsPairTable
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				chain, keyf, root := tlsLeafFiles(t, t.TempDir(), pki, c.leaf)
				r := runNative(t, tlsPairExe(t, mode), "", chain, keyf, root, fmt.Sprint(c.mode), fmt.Sprint(c.suite))
				if got := strings.TrimSpace(r.stdout); !tlsLineMatches(got, c.want) {
					t.Fatalf("got %q (%s), want %q", got, r.stderr, c.want)
				}
			})
		}
	}
}
