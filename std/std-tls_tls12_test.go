// Why: the TLS 1.2 client's PRF, AEAD records and messages need oracles of
// their own before a handshake can be trusted.
// What: TestStdTls_Tls12Prf, TestStdTls_Tls12Records, TestStdTls_Tls12Messages
// and tls12Fixture; the handshakes are in std-tls_tls12peer_test.go.
// How: the PRF against a Go model of RFC 5246 5, AES-GCM against crypto/cipher,
// and every message of a Go-to-Go handshake parsed and written back byte for
// byte.

package std

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	mrand "math/rand"
	"net"
	"strings"
	"sync"
	"testing"
)

func init() { tlsExtraFixtures["tls/tests_tls12fx.mk"] = tls12Fixture }

// tls12Fixture is written to std/tls in every driver's copy of std.
const tls12Fixture = `// Fixture of the std-tls TLS 1.2 tests (cut unit 3): private entries,
// exported.

import std.crypto

/// FxPrf12 is Prf12 under the hash h (256 or 384).
export FxPrf12(h: int, secret: []u8, label: str, seed: []u8, out: mut []u8) {
    hk := if h == 384 { crypto.HashKind.Sha384 } else { crypto.HashKind.Sha256 }
    Prf12(hk, secret, label, seed, out)
}

Fx12Suite(s: int): Suite {
    return SuiteOf(Trunc[u16](s)) else { Suite.Aes128Gcm }
}

Fx12Aead(suite: int, key: []u8, iv: []u8, seq: u64): Aead {
    var x := Aead.New12(Fx12Suite(suite), key, iv)
    x.seq = seq
    return x
}

/// FxSeal12 seals content of type ty as TLS 1.2 record number seq under
/// the AEAD of the TLS 1.3 suite code suite.
export FxSeal12(suite: int, key: []u8, iv: []u8, seq: u64, ty: u8, content: []u8, out: mut []u8): int {
    var x := Fx12Aead(suite, key, iv, seq)
    return x.Seal12(ty, content, out)
}

/// FxOpen12 opens record (header and body) as TLS 1.2 record number seq.
export FxOpen12(suite: int, key: []u8, iv: []u8, seq: u64, record: []u8, out: mut []u8): (n: int, alert: u8) {
    var x := Fx12Aead(suite, key, iv, seq)
    var alert: u8 = 0
    if record.Len() < 5 {
        return (n: 0, alert: AlertDecodeError)
    }
    n := x.Open12(record[0..5], record[5..], out, mut alert) else {
        return (n: 0, alert: alert)
    }
    return (n: n, alert: 0)
}

/// FxRewrite12 parses one whole TLS 1.2 handshake message with its
/// structure's parser and writes it back with its writer; a ServerHello's
/// TLS 1.2 extensions each go through their parser.
export FxRewrite12(msg: []u8, out: mut []u8): Error!int {
    r := try ParseHandshake(msg, 0xFFFFFF) else {
        fail Protocol
    }
    if r.n != msg.Len() {
        fail Protocol
    }
    var scratch: [65536]u8 = [0; 65536]
    var e := Out.New(mut scratch[..])
    var w := Out.New(out)
    m := r.m
    if m.ty == HsServerHello {
        var sh := try ParseServerHello(m.body)
        var it := ExtIter.New(sh.exts)
        for x := try it.Next() {
            e.U16(x.ty)
            at := e.Open(2)
            if x.ty == ExtExtendedMasterSecret {
                try ParseEms(x.data)
            } else if x.ty == ExtRenegotiationInfo {
                e.Vec8(try ParseRenegotiationInfo(x.data))
            } else if x.ty == ExtPointFormats {
                e.Vec8(try ParsePointFormats(x.data))
            } else {
                e.Bytes(x.data)
            }
            e.Close(at, 2)
        }
        sh.exts = e.b[0..e.at]
        WriteServerHello(mut w, &sh)
    } else if m.ty == HsCertificate {
        var it := Certs12.New(try ParseCertificate12(m.body))
        for x := try it.Next() {
            e.Vec24(x)
        }
        WriteCertificate12(mut w, e.b[0..e.at])
    } else if m.ty == HsServerKeyExchange {
        ske := try ParseServerKeyExchange(m.body)
        WriteServerKeyExchange(mut w, &ske)
    } else if m.ty == HsCertificateRequest {
        cr := try ParseCertificateRequest12(m.body)
        WriteCertificateRequest12(mut w, &cr)
    } else if m.ty == HsServerHelloDone {
        try ParseServerHelloDone(m.body)
        WriteServerHelloDone(mut w)
    } else if m.ty == HsClientKeyExchange {
        WriteClientKeyExchange(mut w, try ParseClientKeyExchange(m.body))
    } else if m.ty == HsHelloRequest {
        try ParseHelloRequest(m.body)
        at := OpenMessage(mut w, HsHelloRequest)
        w.Close(at, 3)
    } else if m.ty == HsFinished {
        WriteFinished(mut w, try ParseFinished(m.body, 12))
    } else {
        fail Unsupported
    }
    if w.full || e.full {
        fail Protocol
    }
    return w.at
}

/// FxFuzz12 rewrites msg (FxRewrite12) and the result again, like
/// FxFuzzMessage: "err", "same", "lossy", or "unstable" (a fault). A
/// ServerHello also goes through CheckServerHello12 against a ClientHello
/// offering every suite and ALPN, whose answer is ignored: it must not
/// panic.
export FxFuzz12(msg: []u8, out: mut []u8): str from msg {
    if msg.Len() >= 4 && msg[0] == HsServerHello {
        sh := ParseServerHello(msg[4..]) else {
            return "err"
        }
        suites: [3]u16 = [0xc02b, 0xc02f, 0xcca8]
        protos: [2]str = ["h2", "http/1.1"]
        hello := Hello(suites: suites[..], random: [0; 32], sid: [0; 32], sni: "example.com", alpn: protos[..],
            group: GroupX25519, cookie: b"")
        var alert: u8 = 0
        var chosen := Chosen(alpn: [0; 255], n: 0)
        _ = CheckServerHello12(mut alert, &sh, &hello, mut chosen) else {
            Suite12(aead: Suite.Aes128Gcm, ecdsa: true)
        }
    }
    (left, right) := out.SplitAt(35000)
    n1 := FxRewrite12(msg, left) else {
        return "err"
    }
    n2 := FxRewrite12(left[0..n1], right) else {
        return "unstable"
    }
    if n1 != n2 || !crypto.Equal(left[0..n1], right[0..n2]) {
        return "unstable"
    }
    if n1 != msg.Len() || !crypto.Equal(left[0..n1], msg) {
        return "lossy"
    }
    return "same"
}

/// FxFuzzRecord12 runs b through a TLS 1.2 record path chosen by its first
/// byte: 0, the rest opened as an AES-128-GCM record; 1, the same under
/// ChaCha20-Poly1305; 2, the rest (a type byte and content of at most
/// 16 KiB) sealed and read by ReadHalf.Take12, whose answer must fit the
/// type. "ok", "err", or "bad" for a fault; never a panic.
export FxFuzzRecord12(b: []u8, out: mut []u8): str from b {
    if b.Len() < 2 {
        return "short"
    }
    key: [32]u8 = [1; 32]
    iv: [12]u8 = [2; 12]
    if b[0] == 0 || b[0] == 1 {
        suite := if b[0] == 0 { 4865 } else { 4867 }
        kl := if b[0] == 0 { 16 } else { 32 }
        il := if b[0] == 0 { 4 } else { 12 }
        r := FxOpen12(suite, key[0..kl], iv[0..il], 0, b[1..], out)
        return if r.alert == 0 { "ok" } else { "err" }
    }
    content := b[2..]
    if content.Len() > MaxPlain {
        return "short"
    }
    ty := b[1]
    var tx := Fx12Aead(4865, key[0..16], iv[0..4], 0)
    var rd := ReadHalf.New()
    rd.aead = Fx12Aead(4865, key[0..16], iv[0..4], 0)
    n := tx.Seal12(ty, content, mut rd.buf[..])
    rd.have = n
    var alert: u8 = 0
    rd.Take12(n - 5, mut alert) else e {
        return match e {
            Protocol {
                if ty == TypeAppData || (ty == TypeAlert && content.Len() == 2 && content[0] == 1) { "bad" } else { "err" }
            }
            Alert { if ty == TypeAlert { "err" } else { "bad" } }
            _ { "bad" }
        }
    }
    if ty == TypeAppData {
        if rd.end != content.Len() || !crypto.Equal(rd.plain[0..rd.end], content) {
            return "bad"
        }
        return "ok"
    }
    if ty == TypeAlert && content.Len() == 2 {
        return "ok"
    }
    return "bad"
}
`

// tls12Case is the oracle driver's Case; the rest of the driver is
// tlsDriver's (its helpers and Main).
const tls12Case = `Case(a: &Arena, io: Io, line: str into a): OutOfMemory!str from a {
    var parts := List[str].New(a)
    for p in line.Split(" ") {
        try parts.Push(p)
    }
    op := Field(&parts, 0)
    out := try a.Slice[u8](70000)
    if op == "P" {
        secret := try Unhex(a, Field(&parts, 2))
        label := str.From(try Unhex(a, Field(&parts, 3))) else { "" }
        seed := try Unhex(a, Field(&parts, 4))
        n := Num(&parts, 5)
        tls.FxPrf12(Num(&parts, 1), secret, label, seed, mut out[0..n])
        return try a.Fmt("ok {h}", (h: try Hex(a, out[0..n])))
    }
    if op == "S" {
        key := try Unhex(a, Field(&parts, 2))
        iv := try Unhex(a, Field(&parts, 3))
        content := try Unhex(a, Field(&parts, 6))
        n := tls.FxSeal12(Num(&parts, 1), key, iv, Cast[u64](Num(&parts, 4)), Trunc[u8](Num(&parts, 5)), content, mut out[..])
        return try a.Fmt("ok {h}", (h: try Hex(a, out[0..n])))
    }
    if op == "O" {
        key := try Unhex(a, Field(&parts, 2))
        iv := try Unhex(a, Field(&parts, 3))
        record := try Unhex(a, Field(&parts, 5))
        r := tls.FxOpen12(Num(&parts, 1), key, iv, Cast[u64](Num(&parts, 4)), record, mut out[..])
        if r.alert != 0 {
            return try a.Fmt("err alert {c}", (c: r.alert))
        }
        return try a.Fmt("ok {h}", (h: try Hex(a, out[0..r.n])))
    }
    if op == "W" {
        msg := try Unhex(a, Field(&parts, 1))
        n := tls.FxRewrite12(msg, mut out[..]) else e {
            return try a.Fmt("err {e}", (e))
        }
        return try a.Fmt("ok {h}", (h: try Hex(a, out[0..n])))
    }
    if op == "Z" {
        msg := try Unhex(a, Field(&parts, 1))
        return tls.FxFuzz12(msg, mut out[..])
    }
    if op == "Y" {
        b := try Unhex(a, Field(&parts, 1))
        return tls.FxFuzzRecord12(b, mut out[..])
    }
    return "err unknown op"
}

`

// tls12Driver is the oracle driver: tlsDriver with tls12Case as its Case.
func tls12Driver() string {
	head := tlsDriver[:strings.Index(tlsDriver, "Case(a: &Arena")]
	main := tlsDriver[strings.Index(tlsDriver, "export Main("):]
	return head + tls12Case + main
}

// tls12PeerDriver is tlsPeerDriver with the TLS 1.2 client on
// (min_version Tls12), its Stall transport stalling the 31-byte writes
// that carry an AES-GCM TLS 1.2 alert record.
func tls12PeerDriver() string {
	d := strings.Replace(tlsPeerDriver, "    var cfg := tls.DefaultConfig\n", "    var cfg := tls.DefaultConfig\n    cfg.min_version = tls.Version.Tls12\n", 1)
	return strings.Replace(d, "data.Len() == 24", "data.Len() == 31", 1)
}

var tls12Built struct {
	sync.Mutex
	exe map[string]string
}

// tls12Exe builds the driver of kind ("oracle" or "peer") for mode once
// per test binary.
func tls12Exe(t *testing.T, kind, mode string) string {
	t.Helper()
	tls12Built.Lock()
	defer tls12Built.Unlock()
	if tls12Built.exe == nil {
		tls12Built.exe = map[string]string{}
	}
	if exe, ok := tls12Built.exe[kind+mode]; ok {
		return exe
	}
	src := tls12Driver()
	if kind == "peer" {
		src = tls12PeerDriver()
	}
	exe := tlsBuildDriver(t, "tls12"+kind, mode, src)
	tls12Built.exe[kind+mode] = exe
	return exe
}

// tls12Ask sends records to the mode's oracle driver, one answer each.
func tls12Ask(t *testing.T, mode string, records []string) []string {
	t.Helper()
	r := runNative(t, tls12Exe(t, "oracle", mode), strings.Join(records, "\n")+"\n")
	if r.code != 0 {
		t.Fatalf("driver exit %d: %s", r.code, r.stderr)
	}
	got := strings.Split(strings.TrimRight(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if len(got) != len(records) {
		t.Fatalf("%d answers to %d records: %q", len(got), len(records), r.stdout)
	}
	return got
}

// tls12Check asks every case in both modes and reports each disagreement.
func tls12Check(t *testing.T, cases []tlsCase) {
	t.Helper()
	recs := make([]string, len(cases))
	for i, c := range cases {
		recs[i] = c.record
	}
	for _, mode := range tlsModes {
		got := tls12Ask(t, mode, recs)
		bad := 0
		for i, c := range cases {
			if got[i] != c.want {
				bad++
				if bad <= 10 {
					t.Errorf("%s: %s: got %.120q, want %.120q", mode, c.name, got[i], c.want)
				}
			}
		}
		t.Logf("%s: %d of %d agree", mode, len(cases)-bad, len(cases))
	}
}

// tls12Prf is RFC 5246 5's PRF, the Go model: P_hash over label || seed.
func tls12Prf(h func() hash.Hash, secret []byte, label string, seed []byte, n int) []byte {
	ls := append([]byte(label), seed...)
	mac := func(d []byte) []byte {
		m := hmac.New(h, secret)
		m.Write(d)
		return m.Sum(nil)
	}
	a := mac(ls)
	var out []byte
	for len(out) < n {
		out = append(out, mac(append(append([]byte{}, a...), ls...))...)
		a = mac(a)
	}
	return out[:n]
}

// tls12PrfVector is the SHA-256 PRF vector published on the IETF TLS list
// (secret, seed, label "test label", 100 bytes), as other implementations
// test it; the model must give it too.
var tls12PrfVector = struct{ secret, seed, out string }{
	"9bbe436ba940f017b17652849a71db35",
	"a0ba9f936cda311827a6f796ffd5198c",
	"e3f229ba727be17b8d122620557cd453c2aab21d07c3d495329b52d4e61edb5a6b301791e90d35c9c9a46b4e14baf9af0fa022f7077def17abfd3797c0564bab4fbc91666e9def9b97fce34f796789baa48082d122ee42c5a72e5a5110fff70187347b66",
}

// TestStdTls_Tls12Prf checks Prf12 against the Go model over both hashes,
// every label the client uses, secrets, seeds and output lengths from the
// shortest to beyond the key block, and the published vector.
func TestStdTls_Tls12Prf(t *testing.T) {
	sec, _ := hex.DecodeString(tls12PrfVector.secret)
	seed, _ := hex.DecodeString(tls12PrfVector.seed)
	if got := hex.EncodeToString(tls12Prf(sha256.New, sec, "test label", seed, 100)); got != tls12PrfVector.out {
		t.Fatalf("the Go model misses the published vector: %s", got)
	}
	cases := []tlsCase{{name: "published vector", record: fmt.Sprintf("P 256 %s %s %s 100", tls12PrfVector.secret, tlsHex([]byte("test label")), tls12PrfVector.seed), want: "ok " + tls12PrfVector.out}}
	r := mrand.New(mrand.NewSource(12))
	labels := []string{"extended master secret", "key expansion", "client finished", "server finished", "master secret", "x"}
	for _, h := range []int{256, 384} {
		hf := sha256.New
		if h == 384 {
			hf = sha512.New384
		}
		for _, label := range labels {
			for _, sl := range []int{0, 1, 32, 48, 64} {
				for _, n := range []int{1, 12, 32, 33, 48, 49, 72, 88, 100} {
					secret := make([]byte, 32+r.Intn(17))
					r.Read(secret)
					sd := make([]byte, sl)
					r.Read(sd)
					want := tls12Prf(hf, secret, label, sd, n)
					cases = append(cases, tlsCase{
						name:   fmt.Sprintf("sha%d %q seed %d out %d", h, label, sl, n),
						record: fmt.Sprintf("P %d %s %s %s %d", h, tlsHex(secret), tlsHex([]byte(label)), tlsHex(sd), n),
						want:   "ok " + tlsHex(want),
					})
				}
			}
		}
	}
	tls12Check(t, cases)
}

// tls12Gcm is the Go model of RFC 5288's AES-GCM record (RFC 5246
// 6.2.3.3): salt || explicit nonce, the 13-byte additional data.
func tls12Gcm(key []byte) cipher.AEAD {
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		panic(err)
	}
	return g
}

func tls12Ad(seq uint64, ty byte, n int) []byte {
	ad := binary.BigEndian.AppendUint64(nil, seq)
	return append(ad, ty, 3, 3, byte(n>>8), byte(n))
}

// tls12GcmSeal seals plain as record number seq, the explicit nonce
// explicit.
func tls12GcmSeal(key, salt []byte, seq uint64, explicit []byte, ty byte, plain []byte) []byte {
	nonce := append(append([]byte{}, salt...), explicit...)
	body := append(append([]byte{}, explicit...), tls12Gcm(key).Seal(nil, nonce, plain, tls12Ad(seq, ty, len(plain)))...)
	return append([]byte{ty, 3, 3, byte(len(body) >> 8), byte(len(body))}, body...)
}

// tls12GcmOpen opens a record sealed as record number seq.
func tls12GcmOpen(key, salt []byte, seq uint64, rec []byte) ([]byte, error) {
	if len(rec) < 5+8+16 {
		return nil, fmt.Errorf("short record")
	}
	body := rec[5:]
	nonce := append(append([]byte{}, salt...), body[:8]...)
	return tls12Gcm(key).Open(nil, nonce, body[8:], tls12Ad(seq, rec[0], len(body)-24))
}

// TestStdTls_Tls12Records checks Aead.Seal12 and Open12: AES-128-GCM and
// AES-256-GCM records sealed by Mk open in the Go model and the model's
// open in Mk, an explicit nonce other than the sequence number included;
// a forged tag, a wrong sequence number, a short body and an oversized
// record are refused; ChaCha20-Poly1305 records (RFC 7905: no explicit
// nonce) round-trip and refuse a forged tag.
func TestStdTls_Tls12Records(t *testing.T) {
	r := mrand.New(mrand.NewSource(1212))
	var cases []tlsCase
	for _, kl := range []int{16, 32} {
		suite := 4865
		if kl == 32 {
			suite = 4866
		}
		key := make([]byte, kl)
		salt := make([]byte, 4)
		r.Read(key)
		r.Read(salt)
		for _, n := range []int{0, 1, 5, 100, 16384} {
			for _, seq := range []uint64{0, 1, 0x0102030405060708} {
				for _, ty := range []byte{21, 22, 23} {
					plain := make([]byte, n)
					r.Read(plain)
					explicit := binary.BigEndian.AppendUint64(nil, seq)
					want := tls12GcmSeal(key, salt, seq, explicit, ty, plain)
					name := fmt.Sprintf("gcm%d n=%d seq=%d ty=%d", kl*8, n, seq, ty)
					if n == 16384 && ty != 23 {
						continue
					}
					cases = append(cases, tlsCase{name: name + " seal", record: fmt.Sprintf("S %d %s %s %d %d %s", suite, tlsHex(key), tlsHex(salt), seq, ty, tlsHex(plain)), want: "ok " + tlsHex(want)})
					other := make([]byte, 8)
					r.Read(other)
					rec := tls12GcmSeal(key, salt, seq, other, ty, plain)
					cases = append(cases, tlsCase{name: name + " open", record: fmt.Sprintf("O %d %s %s %d %s", suite, tlsHex(key), tlsHex(salt), seq, tlsHex(rec)), want: "ok " + tlsHex(plain)})
					if n == 100 && seq == 1 {
						bad := append([]byte{}, rec...)
						bad[len(bad)-1] ^= 1
						cases = append(cases, tlsCase{name: name + " forged tag", record: fmt.Sprintf("O %d %s %s %d %s", suite, tlsHex(key), tlsHex(salt), seq, tlsHex(bad)), want: "err alert 20"})
						cases = append(cases, tlsCase{name: name + " wrong seq", record: fmt.Sprintf("O %d %s %s %d %s", suite, tlsHex(key), tlsHex(salt), seq+1, tlsHex(rec)), want: "err alert 20"})
						typed := append([]byte{}, rec...)
						typed[0] ^= 3
						cases = append(cases, tlsCase{name: name + " type changed", record: fmt.Sprintf("O %d %s %s %d %s", suite, tlsHex(key), tlsHex(salt), seq, tlsHex(typed)), want: "err alert 20"})
					}
				}
			}
		}
		short := []byte{23, 3, 3, 0, 23}
		short = append(short, make([]byte, 23)...)
		cases = append(cases, tlsCase{name: fmt.Sprintf("gcm%d body under 24 bytes", kl*8), record: fmt.Sprintf("O %d %s %s 0 %s", suite, tlsHex(key), tlsHex(salt), tlsHex(short)), want: "err alert 20"})
		big := tls12GcmSeal(key, salt, 0, make([]byte, 8), 23, make([]byte, 16385))
		cases = append(cases, tlsCase{name: fmt.Sprintf("gcm%d content over 2^14", kl*8), record: fmt.Sprintf("O %d %s %s 0 %s", suite, tlsHex(key), tlsHex(salt), tlsHex(big)), want: "err alert 22"})
	}
	key := make([]byte, 32)
	iv := make([]byte, 12)
	r.Read(key)
	r.Read(iv)
	var recs []string
	var plains [][]byte
	for _, n := range []int{0, 1, 300, 16384} {
		p := make([]byte, n)
		r.Read(p)
		plains = append(plains, p)
		recs = append(recs, fmt.Sprintf("S 4867 %s %s 7 23 %s", tlsHex(key), tlsHex(iv), tlsHex(p)))
	}
	for _, mode := range tlsModes {
		got := tls12Ask(t, mode, recs)
		for i, g := range got {
			rec, err := hex.DecodeString(strings.TrimPrefix(g, "ok "))
			if err != nil || len(rec) != 5+len(plains[i])+16 {
				t.Fatalf("%s: chacha seal of %d bytes: %.80q", mode, len(plains[i]), g)
			}
			cases = append(cases, tlsCase{name: fmt.Sprintf("%s chacha n=%d round trip", mode, len(plains[i])), record: fmt.Sprintf("O 4867 %s %s 7 %s", tlsHex(key), tlsHex(iv), tlsHex(rec)), want: "ok " + tlsHex(plains[i])})
			rec[len(rec)-3] ^= 0x40
			cases = append(cases, tlsCase{name: fmt.Sprintf("%s chacha n=%d forged", mode, len(plains[i])), record: fmt.Sprintf("O 4867 %s %s 7 %s", tlsHex(key), tlsHex(iv), tlsHex(rec)), want: "err alert 20"})
		}
	}
	tls12Check(t, cases)
}

// tls12Records splits a stream into its records.
func tls12Records(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 5 {
		n := 5 + (int(b[3])<<8 | int(b[4]))
		if len(b) < n {
			break
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

// tls12Messages is the handshake messages of a stream's plaintext
// handshake records before its first ChangeCipherSpec.
func tls12Messages(b []byte) [][]byte {
	var hs []byte
	for _, r := range tls12Records(b) {
		if r[0] == 20 {
			break
		}
		if r[0] == 22 {
			hs = append(hs, r[5:]...)
		}
	}
	var out [][]byte
	for len(hs) >= 4 {
		n := 4 + (int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3]))
		if len(hs) < n {
			break
		}
		out = append(out, hs[:n])
		hs = hs[n:]
	}
	return out
}

// tls12RecConn records what passes through a connection, each direction
// on its own.
type tls12RecConn struct {
	net.Conn
	mu      sync.Mutex
	in, out bytes.Buffer
}

func (c *tls12RecConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.mu.Lock()
	c.in.Write(b[:n])
	c.mu.Unlock()
	return n, err
}

func (c *tls12RecConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	c.out.Write(b)
	c.mu.Unlock()
	return c.Conn.Write(b)
}

// tls12GoFlight runs a Go client against a Go server at TLS 1.2 with cfg
// over a recording connection and returns the server's and the client's
// handshake messages before their ChangeCipherSpec.
func tls12GoFlight(t *testing.T, cfg *tls.Config) (server, client [][]byte) {
	t.Helper()
	a, b := net.Pipe()
	rec := &tls12RecConn{Conn: a}
	done := make(chan error, 1)
	go func() {
		s := tls.Server(rec, cfg)
		done <- s.Handshake()
		s.Close()
	}()
	cc := &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12, SessionTicketsDisabled: true}
	c := tls.Client(b, cc)
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.Close()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return tls12Messages(rec.out.Bytes()), tls12Messages(rec.in.Bytes())
}

// TestStdTls_Tls12Messages parses every TLS 1.2 message of Go-to-Go
// handshakes (each leaf key, a CertificateRequest, ALPN) with its
// structure's parser and writes it back byte for byte; the client's
// ClientKeyExchange and empty Certificate included; malformed messages
// are refused.
func TestStdTls_Tls12Messages(t *testing.T) {
	pki := tlsPeerPKI(t)
	var cases []tlsCase
	seen := map[byte]int{}
	for _, leaf := range []string{"p256", "rsa", "ed25519"} {
		for _, req := range []bool{false, true} {
			cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves[leaf]}, MaxVersion: tls.VersionTLS12, NextProtos: []string{"h2"}}
			if req {
				cfg.ClientAuth = tls.RequestClientCert
			}
			server, client := tls12GoFlight(t, cfg)
			for _, m := range append(server, client[1:]...) {
				seen[m[0]]++
				cases = append(cases, tlsCase{name: fmt.Sprintf("%s request=%v type %d", leaf, req, m[0]), record: "W " + tlsHex(m), want: "ok " + tlsHex(m)})
			}
		}
	}
	for _, ty := range []byte{2, 11, 12, 13, 14, 16} {
		if seen[ty] == 0 {
			t.Errorf("no message of type %d captured", ty)
		}
	}
	fin := append([]byte{20, 0, 0, 12}, bytes.Repeat([]byte{5}, 12)...)
	more := []tlsCase{
		{name: "HelloRequest", record: "W 00000000", want: "ok 00000000"},
		{name: "Finished", record: "W " + tlsHex(fin), want: "ok " + tlsHex(fin)},
		{name: "HelloRequest with a body", record: "W 0000000100", want: "err Protocol"},
		{name: "ServerHelloDone with a body", record: "W 0e00000100", want: "err Protocol"},
		{name: "Finished of 11 bytes", record: "W 1400000b" + strings.Repeat("05", 11), want: "err Protocol"},
		{name: "ServerKeyExchange explicit curve", record: "W 0c00000a" + "01" + "001d" + "0101" + "0403" + "0001" + "00", want: "err Protocol"},
		{name: "ServerKeyExchange empty point", record: "W 0c000008" + "03" + "001d" + "00" + "0403" + "0000", want: "err Protocol"},
		{name: "ServerKeyExchange trailing byte", record: "W 0c00000a" + "03" + "001d" + "0101" + "0403" + "0000" + "00", want: "err Protocol"},
		{name: "CertificateRequest odd schemes", record: "W 0d000009" + "0101" + "0003" + "040305" + "0000", want: "err Protocol"},
		{name: "CertificateRequest no types", record: "W 0d000007" + "00" + "0002" + "0403" + "0000", want: "err Protocol"},
		{name: "Certificate with an empty entry", record: "W 0b000006" + "000003" + "000000", want: "err Protocol"},
		{name: "Certificate list longer than the body", record: "W 0b000007" + "000009" + "000001" + "00", want: "err Protocol"},
		{name: "ClientKeyExchange empty point", record: "W 10000001" + "00", want: "err Protocol"},
		{name: "empty Certificate", record: "W 0b000003000000", want: "ok 0b000003000000"},
	}
	tls12Check(t, append(cases, more...))
}
