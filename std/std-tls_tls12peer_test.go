// Why: the TLS 1.2 client must interoperate with a real server and refuse
// server flights an honest server never sends.
// What: TestStdTls_Tls12GoPeer, TestStdTls_Tls12Refused,
// TestStdTls_Tls12HelloRequestRefused, TestStdTls_Tls12Linux,
// TestStdTls_Tls12OneByteRecords, FuzzStdTls_Messages12 and
// FuzzStdTls_Records12.
// How: Go's crypto/tls server limited to TLS 1.2, and a hand-built server
// flight for each refusal (no extended master secret, downgrade sentinel, bad
// signature, a curve or scheme not offered, a low-order point, an early
// ChangeCipherSpec).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tls12PeerCase is one run of the TLS 1.2 driver against Go's server.
type tls12PeerCase struct {
	name     string
	leaf     string
	roots    string
	host     string
	alpn     string
	mode     string
	n        int
	suite    uint16 // the one suite Go's server allows
	curves   []tls.CurveID
	protos   []string
	max      uint16 // Go's MaxVersion, TLS 1.2 by default
	pkcs1    bool   // the RSA leaf signs with PKCS#1 v1.5 only
	want     string
	version  uint16
	curve    tls.CurveID
	goErr    string
	closeEOF bool
	exe      string // "peer12" (default) or "peer13", the TLS 1.3-only driver
}

func tls12RunPeer(t *testing.T, mode string, pki *tlsPKI, c tls12PeerCase) {
	t.Helper()
	cert := pki.leaves[c.leaf]
	if c.pkcs1 {
		cert.SupportedSignatureAlgorithms = []tls.SignatureScheme{tls.PKCS1WithSHA256}
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MaxVersion: tls.VersionTLS12, CurvePreferences: c.curves, NextProtos: c.protos}
	if c.max != 0 {
		cfg.MaxVersion = c.max
	}
	if c.suite != 0 {
		cfg.CipherSuites = []uint16{c.suite}
	}
	addr, done := tlsGoServer(t, cfg)
	roots := filepath.Join(t.TempDir(), "root.pem")
	pemText := pki.rootPEM
	if c.roots == "other" {
		pemText = pki.otherPEM
	}
	os.WriteFile(roots, []byte(pemText), 0o644)
	host := c.host
	if host == "" {
		host = "localhost"
	}
	alpn, m := c.alpn, c.mode
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
	exe := tls12Exe(t, "peer", mode)
	if c.exe == "peer13" {
		exe = tlsPeerExe(t, mode)
	}
	r := runNative(t, exe, "", addr, host, roots, "-", alpn, m, "0", "300", fmt.Sprint(n))
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
		want := c.version
		if want == 0 {
			want = tls.VersionTLS12
		}
		if s.state.Version != want {
			t.Errorf("version %x, want %x", s.state.Version, want)
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

// TestStdTls_Tls12GoPeer runs the TLS 1.2 client against Go's crypto/tls
// server limited to TLS 1.2: every suite over X25519 and over P-256, each
// leaf key (P-256, P-384, Ed25519, RSA with PSS and with PKCS#1 v1.5),
// ALPN, 1 MiB both ways, close_notify, the connector, an unknown
// authority, Close past its limit, a TLS 1.3 server still chosen at TLS
// 1.3, and the default TLS 1.3-only client refusing a TLS 1.2 server.
func TestStdTls_Tls12GoPeer(t *testing.T) {
	pki := tlsPeerPKI(t)
	var cases []tls12PeerCase
	suites := []struct {
		id   uint16
		name string
		leaf string
	}{
		{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, "ecdsa aes128gcm", "p256"},
		{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, "rsa aes128gcm", "rsa"},
		{tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, "ecdsa aes256gcm", "p256"},
		{tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, "rsa aes256gcm", "rsa"},
		{tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, "ecdsa chacha20poly1305", "p256"},
		{tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256, "rsa chacha20poly1305", "rsa"},
	}
	for _, s := range suites {
		for _, g := range []tls.CurveID{tls.X25519, tls.CurveP256} {
			cases = append(cases, tls12PeerCase{name: fmt.Sprintf("%s %v", s.name, g), leaf: s.leaf, suite: s.id, curves: []tls.CurveID{g}, curve: g, want: "ok alpn=- n=5", closeEOF: true})
		}
	}
	ecdsa128 := tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
	cases = append(cases,
		tls12PeerCase{name: "leaf p384", leaf: "p384", suite: ecdsa128, want: "ok alpn=- n=5"},
		tls12PeerCase{name: "leaf ed25519", leaf: "ed25519", suite: ecdsa128, want: "ok alpn=- n=5"},
		tls12PeerCase{name: "leaf rsa pkcs1", leaf: "rsa", pkcs1: true, suite: tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, want: "ok alpn=- n=5"},
		tls12PeerCase{name: "alpn", leaf: "p256", alpn: "http/1.1,h2", protos: []string{"h2", "http/1.1"}, want: "ok alpn=h2 n=5"},
		tls12PeerCase{name: "one MiB both ways chacha", leaf: "p256", suite: tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, n: 1 << 20, want: "ok alpn=- n=1048576", closeEOF: true},
		tls12PeerCase{name: "one MiB both ways gcm", leaf: "p256", suite: ecdsa128, n: 1 << 20, want: "ok alpn=- n=1048576", closeEOF: true},
		tls12PeerCase{name: "connector", leaf: "p256", mode: "connector", want: "ok alpn=- n=5", closeEOF: true},
		tls12PeerCase{name: "unknown authority", leaf: "p256", roots: "other", want: "err UnknownAuthority", goErr: "unknown certificate authority"},
		tls12PeerCase{name: "name mismatch", leaf: "p256", host: "other.test", want: "err NameMismatch", goErr: "bad certificate"},
		tls12PeerCase{name: "close past its limit", leaf: "p256", suite: ecdsa128, mode: "stall", want: "closeerr TimedOut ms=*"},
		tls12PeerCase{name: "close past its limit under NoCancel", leaf: "p256", suite: ecdsa128, mode: "stallnc", want: "closeerr TimedOut ms=*"},
		tls12PeerCase{name: "a TLS 1.3 server still speaks TLS 1.3", leaf: "p256", max: tls.VersionTLS13, version: tls.VersionTLS13, want: "ok alpn=- n=5", closeEOF: true},
		tls12PeerCase{name: "TLS 1.2 off by default", leaf: "p256", exe: "peer13", want: "err Alert(code: 70)", goErr: "unsupported versions"},
	)
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) { tls12RunPeer(t, mode, pki, c) })
		}
	}
}

// tls12Knobs shape the hand-built TLS 1.2 server flight.
type tls12Knobs struct {
	noEMS     bool
	downgrade bool
	reneg     []byte // renegotiation_info's renegotiated_connection
	formats   []byte // ec_point_formats, nil for {0}
	extra     uint16 // an extension no client asked for
	suite     uint16 // 0xc02b by default
	curve     uint16 // X25519 by default
	point     []byte // the server's share, a fresh X25519 key's by default
	scheme    uint16 // 0x0403 by default
	badSig    bool
	earlyCCS  bool
	stall     bool // stop after ServerHello
}

// tls12Hs is a handshake message of type ty around body.
func tls12Hs(ty byte, body []byte) []byte {
	n := len(body)
	return append([]byte{ty, byte(n >> 16), byte(n >> 8), byte(n)}, body...)
}

// tls12Record is a plaintext record of type ty around body.
func tls12Record(ty byte, body []byte) []byte {
	return append([]byte{ty, 3, 3, byte(len(body) >> 8), byte(len(body))}, body...)
}

// tls12Flight builds a server's first flight answering hello (a
// ClientHello message) with the P-256 leaf: ServerHello, Certificate,
// ServerKeyExchange signed over both randoms, ServerHelloDone, as the
// knobs bend it; nil (a stall) when k.stall, after the ServerHello.
func tls12Flight(t *testing.T, pki *tlsPKI, k tls12Knobs) func(hello []byte) []byte {
	return func(hello []byte) []byte {
		if len(hello) < 38 {
			return nil
		}
		crand := hello[6:38]
		srand := bytes.Repeat([]byte{7}, 32)
		if k.downgrade {
			copy(srand[24:], "DOWNGRD\x01")
		}
		suite := k.suite
		if suite == 0 {
			suite = 0xc02b
		}
		sh := []byte{3, 3}
		sh = append(sh, srand...)
		sh = append(sh, 0, byte(suite>>8), byte(suite), 0)
		var ext []byte
		if !k.noEMS {
			ext = append(ext, 0, 23, 0, 0)
		}
		if k.reneg != nil {
			ext = append(ext, 0xff, 1, 0, byte(1+len(k.reneg)), byte(len(k.reneg)))
			ext = append(ext, k.reneg...)
		}
		formats := k.formats
		if formats == nil {
			formats = []byte{0}
		}
		ext = append(ext, 0, 11, 0, byte(1+len(formats)), byte(len(formats)))
		ext = append(ext, formats...)
		if k.extra != 0 {
			ext = append(ext, byte(k.extra>>8), byte(k.extra), 0, 0)
		}
		sh = append(sh, byte(len(ext)>>8), byte(len(ext)))
		sh = append(sh, ext...)
		out := tls12Record(22, tls12Hs(2, sh))
		if k.stall {
			return append(out, 0) // never a whole second record
		}
		if k.earlyCCS {
			out = append(out, tls12Record(20, []byte{1})...)
		}
		leaf := pki.leaves["p256"]
		var list []byte
		for _, der := range leaf.Certificate {
			list = append(list, byte(len(der)>>16), byte(len(der)>>8), byte(len(der)))
			list = append(list, der...)
		}
		cert := append([]byte{byte(len(list) >> 16), byte(len(list) >> 8), byte(len(list))}, list...)
		curve := k.curve
		if curve == 0 {
			curve = 29
		}
		point := k.point
		if point == nil {
			priv, err := ecdh.X25519().GenerateKey(rand.Reader)
			if err != nil {
				t.Error(err)
				return nil
			}
			point = priv.PublicKey().Bytes()
		}
		params := append([]byte{3, byte(curve >> 8), byte(curve), byte(len(point))}, point...)
		scheme := k.scheme
		if scheme == 0 {
			scheme = 0x0403
		}
		d := sha256.Sum256(append(append(append([]byte{}, crand...), srand...), params...))
		sig, err := ecdsa.SignASN1(rand.Reader, leaf.PrivateKey.(*ecdsa.PrivateKey), d[:])
		if err != nil {
			t.Error(err)
			return nil
		}
		if k.badSig {
			d[0] ^= 1
			sig, _ = ecdsa.SignASN1(rand.Reader, leaf.PrivateKey.(*ecdsa.PrivateKey), d[:])
		}
		ske := append(params, byte(scheme>>8), byte(scheme), byte(len(sig)>>8), byte(len(sig)))
		ske = append(ske, sig...)
		var msgs []byte
		msgs = append(msgs, tls12Hs(11, cert)...)
		msgs = append(msgs, tls12Hs(12, ske)...)
		msgs = append(msgs, tls12Hs(14, nil)...)
		return append(out, tls12Record(22, msgs)...)
	}
}

// TestStdTls_Tls12Refused checks the refusals an honest Go server never
// provokes, each from a hand-built server flight: the error the client
// reports and the alert it sends.
func TestStdTls_Tls12Refused(t *testing.T) {
	pki := tlsPeerPKI(t)
	cases := []struct {
		name  string
		k     tls12Knobs
		want  string
		alert byte
		hs    string
	}{
		{"no extended master secret", tls12Knobs{noEMS: true}, "err Unsupported", 40, "0"},
		{"downgrade sentinel", tls12Knobs{downgrade: true}, "err Protocol", 47, "0"},
		{"renegotiation_info not empty", tls12Knobs{reneg: []byte{1, 2, 3}}, "err Protocol", 40, "0"},
		{"point formats without uncompressed", tls12Knobs{formats: []byte{1, 2}}, "err Protocol", 47, "0"},
		{"extension not asked for", tls12Knobs{extra: 35}, "err Protocol", 110, "0"},
		{"suite not offered", tls12Knobs{suite: 0xc013}, "err Protocol", 47, "0"},
		{"rsa suite with an ecdsa leaf", tls12Knobs{suite: 0xc02f}, "err Protocol", 43, "0"},
		{"bad ServerKeyExchange signature", tls12Knobs{badSig: true}, "err Protocol", 51, "0"},
		{"scheme not offered", tls12Knobs{scheme: 0x0603}, "err Protocol", 47, "0"},
		{"curve not offered", tls12Knobs{curve: 24, point: append([]byte{4}, make([]byte, 96)...)}, "err Protocol", 47, "0"},
		{"x25519 point of 31 bytes", tls12Knobs{point: make([]byte, 31)}, "err Protocol", 47, "0"},
		{"low-order x25519 point", tls12Knobs{point: make([]byte, 32)}, "err Protocol", 47, "0"},
		{"p256 point off the curve", tls12Knobs{curve: 23, point: append([]byte{4}, bytes.Repeat([]byte{1}, 64)...)}, "err Protocol", 47, "0"},
		{"early ChangeCipherSpec", tls12Knobs{earlyCCS: true}, "err Protocol", 10, "0"},
		{"stall after ServerHello", tls12Knobs{stall: true}, "err TimedOut", 0, "300"},
	}
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				addr, back := tlsRawServerFn(t, nil, tls12Flight(t, pki, c.k))
				roots := filepath.Join(t.TempDir(), "root.pem")
				os.WriteFile(roots, []byte(pki.rootPEM), 0o644)
				start := time.Now()
				r := runNative(t, tls12Exe(t, "peer", mode), "", addr, "localhost", roots, "-", "-", "plain", c.hs, "300", "5")
				got := strings.TrimSpace(r.stdout)
				if got != c.want {
					t.Fatalf("got %q (%s), want %q", got, r.stderr, c.want)
				}
				if c.hs == "300" && time.Since(start) > 5*time.Second {
					t.Errorf("a 300 ms handshake limit took %v", time.Since(start))
				}
				if c.alert == 0 {
					return
				}
				b := <-back
				if want := tlsAlertRecord(c.alert); !bytes.HasPrefix(b, want) {
					t.Errorf("the client sent %x, want the alert %x", b, want)
				}
			})
		}
	}
}

// TestStdTls_Tls12HelloRequestRefused completes a TLS 1.2 handshake with
// Go's server, then sends a HelloRequest sealed with the server's keys
// (derived from the master secret Go's KeyLogWriter gives, by the Go
// model): the client's Read fails with Protocol and the client sends
// no_renegotiation (100) at the warning level, found by opening its
// records with the client's keys.
func TestStdTls_Tls12HelloRequestRefused(t *testing.T) {
	pki := tlsPeerPKI(t)
	for _, mode := range tlsModes {
		t.Run(mode, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			var keylog bytes.Buffer
			cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}, MaxVersion: tls.VersionTLS12,
				CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256}, KeyLogWriter: &keylog}
			type result struct {
				in  []byte
				out []byte
				err error
			}
			done := make(chan result, 1)
			go func() {
				raw, err := l.Accept()
				if err != nil {
					done <- result{err: err}
					return
				}
				defer raw.Close()
				rec := &tls12RecConn{Conn: raw}
				raw.SetDeadline(time.Now().Add(20 * time.Second))
				s := tls.Server(rec, cfg)
				if err := s.Handshake(); err != nil {
					done <- result{err: err}
					return
				}
				rec.mu.Lock()
				out := append([]byte{}, rec.out.Bytes()...)
				rec.mu.Unlock()
				line := strings.Fields(keylog.String())
				if len(line) != 3 || line[0] != "CLIENT_RANDOM" {
					done <- result{err: fmt.Errorf("key log %q", keylog.String())}
					return
				}
				crand, _ := hex.DecodeString(line[1])
				master, _ := hex.DecodeString(line[2])
				msgs := tls12Messages(out)
				srand := msgs[0][6:38]
				block := tls12Prf(sha256.New, master, "key expansion", append(append([]byte{}, srand...), crand...), 40)
				skey, ssalt := block[16:32], block[36:40]
				// The server's Finished was its record 0.
				hr := tls12GcmSeal(skey, ssalt, 1, binary.BigEndian.AppendUint64(nil, 1), 22, []byte{0, 0, 0, 0})
				raw.Write(hr)
				raw.SetReadDeadline(time.Now().Add(5 * time.Second))
				io.Copy(io.Discard, rec)
				rec.mu.Lock()
				in := append([]byte{}, rec.in.Bytes()...)
				rec.mu.Unlock()
				done <- result{in: in, out: block}
			}()
			r := runNative(t, tls12Exe(t, "peer", mode), "", l.Addr().String(), "localhost", tls12RootFile(t, pki), "-", "-", "plain", "0", "300", "5")
			got := strings.TrimSpace(r.stdout)
			if got != "err Protocol" {
				t.Errorf("got %q (%s), want err Protocol", got, r.stderr)
			}
			res := <-done
			if res.err != nil {
				t.Fatal(res.err)
			}
			ckey, csalt := res.out[0:16], res.out[32:36]
			var seq uint64
			var after bool
			var alerts [][]byte
			for _, rec := range tls12Records(res.in) {
				if rec[0] == 20 {
					after = true
					continue
				}
				if !after {
					continue
				}
				p, err := tls12GcmOpen(ckey, csalt, seq, rec)
				if err != nil {
					t.Fatalf("the client's record %d does not open: %v", seq, err)
				}
				seq++
				if rec[0] == 21 {
					alerts = append(alerts, p)
				}
			}
			if len(alerts) != 1 || !bytes.Equal(alerts[0], []byte{1, 100}) {
				t.Errorf("the client's alerts %x, want one warning no_renegotiation (0164)", alerts)
			}
		})
	}
}

// tls12RootFile writes the test root to a file of the test's own.
func tls12RootFile(t *testing.T, pki *tlsPKI) string {
	p := filepath.Join(t.TempDir(), "root.pem")
	os.WriteFile(p, []byte(pki.rootPEM), 0o644)
	return p
}

// tls12FuzzSeeds are TLS 1.2 messages of every structure the client
// parses, hand-built (the Go-to-Go captures of TestStdTls_Tls12Messages
// need a test's PKI).
func tls12FuzzSeeds() [][]byte {
	sh := append([]byte{3, 3}, bytes.Repeat([]byte{7}, 32)...)
	sh = append(sh, 0, 0xc0, 0x2b, 0, 0, 19, 0, 23, 0, 0, 0xff, 1, 0, 1, 0, 0, 11, 0, 2, 1, 0, 0, 35, 0, 0)
	cert := []byte{0, 0, 10, 0, 0, 7, 0x30, 5, 2, 1, 1, 5, 0}
	point := bytes.Repeat([]byte{9}, 32)
	ske := append([]byte{3, 0, 29, 32}, point...)
	ske = append(ske, 4, 3, 0, 8, 0x30, 6, 2, 1, 1, 2, 1, 1)
	cr := []byte{1, 1, 0, 4, 4, 3, 8, 4, 0, 0}
	cke := append([]byte{65, 4}, bytes.Repeat([]byte{3}, 64)...)
	return [][]byte{tls12Hs(2, sh), tls12Hs(11, cert), tls12Hs(11, []byte{0, 0, 0}), tls12Hs(12, ske), tls12Hs(13, cr),
		tls12Hs(14, nil), tls12Hs(16, cke), tls12Hs(20, bytes.Repeat([]byte{1}, 12)), tls12Hs(0, nil)}
}

// tls12FuzzOne runs one record through the release oracle driver.
func tls12FuzzOne(t *testing.T, record string) string {
	t.Helper()
	r := runNative(t, tls12Exe(t, "oracle", "release"), record+"\n")
	if r.code != 0 {
		t.Fatalf("driver exit %d on %.80s\nstderr: %s", r.code, record, r.stderr)
	}
	return strings.TrimSpace(r.stdout)
}

// FuzzStdTls_Messages12 runs any bytes as a TLS 1.2 handshake message,
// parsed by its structure's parser (a ServerHello also through
// CheckServerHello12), written back, and the result rewritten again: a
// panic, a hang or an unstable rewrite fails.
func FuzzStdTls_Messages12(f *testing.F) {
	for _, m := range tls12FuzzSeeds() {
		f.Add(m)
		f.Add(m[:len(m)/2])
	}
	f.Fuzz(func(t *testing.T, msg []byte) {
		if len(msg) > 16384 {
			return
		}
		switch got := tls12FuzzOne(t, "Z "+tlsHex(msg)); got {
		case "err", "same", "lossy":
		default:
			t.Fatalf("%x: %s", msg, got)
		}
	})
}

// FuzzStdTls_Records12: any bytes opened as a TLS 1.2 AES-GCM or
// ChaCha20-Poly1305 record, or sealed with any type and read by
// ReadHalf.Take12, whose answer must fit the type.
func FuzzStdTls_Records12(f *testing.F) {
	f.Add([]byte{0, 23, 3, 3, 0, 24, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{1, 23, 3, 3, 0, 17, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	f.Add([]byte{2, 23, 'h', 'i'})
	f.Add([]byte{2, 21, 1, 0})
	f.Add([]byte{2, 21, 2, 40})
	f.Add([]byte{2, 21, 1, 90})
	f.Add([]byte{2, 22, 0, 0, 0, 0})
	f.Add([]byte{2, 20, 1})
	f.Add([]byte{2, 23})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 17000 {
			return
		}
		if got := tls12FuzzOne(t, "Y "+tlsHex(b)); got == "bad" || strings.HasPrefix(got, "err unknown") {
			t.Fatalf("%x: %s", b, got)
		}
	})
}

// TestStdTls_Tls12Linux runs the TLS 1.2 client on x86_64-linux, dev and
// release, in one wsl.exe client: the PRF and record oracle cases, and
// handshakes against Go's crypto/tls server limited to TLS 1.2 (the Linux
// helper of std-tls_wsl_test.go with its version bound changed) over both
// groups and the P-256, RSA and Ed25519 leaves, 1 MiB both ways, and the
// connector. MK_LINUX=1, under tools/wsl-lock.ps1 with go test -p 1.
func TestStdTls_Tls12Linux(t *testing.T) {
	linuxGate(t)
	dir, err := packageTempDir("mktls12linux")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tlsgo.go")
	helperSrc := strings.Replace(tlsLinuxHelper, "MinVersion: tls.VersionTLS13,", "MaxVersion: tls.VersionTLS12,", 1)
	if helperSrc == tlsLinuxHelper {
		t.Fatal("the Linux helper's version bound moved")
	}
	if err := os.WriteFile(src, []byte(helperSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "tlsgo")
	cmd := exec.Command("go", "build", "-o", helper, src)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the Linux helper: %v\n%s", err, o)
	}
	pki := tlsPeerPKI(t)
	leaves := map[string][3]string{}
	for _, l := range []string{"p256", "ed25519", "rsa"} {
		c, k, r := tlsLeafFiles(t, dir, pki, l)
		leaves[l] = [3]string{c, k, r}
	}
	// The oracle: a slice of the Windows cases, recomputed by the Go model.
	var oracle []tlsCase
	r := mrand.New(mrand.NewSource(1213))
	for _, h := range []int{256, 384} {
		hf := sha256.New
		if h == 384 {
			hf = sha512.New384
		}
		for _, n := range []int{12, 48, 88} {
			secret := make([]byte, 48)
			seed := make([]byte, 64)
			r.Read(secret)
			r.Read(seed)
			oracle = append(oracle, tlsCase{name: fmt.Sprintf("prf sha%d %d", h, n), record: fmt.Sprintf("P %d %s %s %s %d", h, tlsHex(secret), tlsHex([]byte("key expansion")), tlsHex(seed), n), want: "ok " + tlsHex(tls12Prf(hf, secret, "key expansion", seed, n))})
		}
	}
	key, salt, plain := make([]byte, 16), make([]byte, 4), make([]byte, 1000)
	r.Read(key)
	r.Read(salt)
	r.Read(plain)
	rec := tls12GcmSeal(key, salt, 9, binary.BigEndian.AppendUint64(nil, 9), 23, plain)
	oracle = append(oracle,
		tlsCase{name: "gcm seal", record: fmt.Sprintf("S 4865 %s %s 9 23 %s", tlsHex(key), tlsHex(salt), tlsHex(plain)), want: "ok " + tlsHex(rec)},
		tlsCase{name: "gcm open", record: fmt.Sprintf("O 4865 %s %s 9 %s", tlsHex(key), tlsHex(salt), tlsHex(rec)), want: "ok " + tlsHex(plain)})
	var in strings.Builder
	for _, c := range oracle {
		in.WriteString(c.record + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "oracle.in"), []byte(in.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	peers := []tlsLinuxPeer{
		{name: "p256 x25519", leaf: "p256", curves: "29", want: "ok alpn=- n=5", goWant: "ok version=303 suite=c02b curve=29 eof=true"},
		{name: "p256 p256", leaf: "p256", curves: "23", want: "ok alpn=- n=5", goWant: "ok version=303 suite=c02b curve=23 eof=true"},
		{name: "rsa", leaf: "rsa", want: "ok alpn=- n=5", goWant: "ok version=303 suite=c02f*"},
		{name: "ed25519", leaf: "ed25519", want: "ok alpn=- n=5", goWant: "ok version=303 suite=c02b*"},
		{name: "one MiB both ways", leaf: "p256", n: 1 << 20, want: "ok alpn=- n=1048576", goWant: "ok version=303*eof=true"},
		{name: "connector", leaf: "p256", mode: "connector", want: "ok alpn=- n=5", goWant: "ok version=303*"},
	}
	w := rtsWSLPath
	linux := []string{"--target", "x86_64-linux"}
	var sh strings.Builder
	sh.WriteString("#!/bin/sh\n")
	for _, mode := range tlsModes {
		drv := tlsBuildDriver(t, "tls12oracle-linux", mode, tls12Driver(), linux...)
		peer := tlsBuildDriver(t, "tls12peer-linux", mode, tls12PeerDriver(), linux...)
		fmt.Fprintf(&sh, "echo 'BEGIN %s oracle'\n%s < %s\necho \"END $?\"\n", mode, w(drv), w(filepath.Join(dir, "oracle.in")))
		for _, c := range peers {
			l := leaves[c.leaf]
			n := c.n
			if n == 0 {
				n = 5
			}
			m := c.mode
			if m == "" {
				m = "plain"
			}
			fmt.Fprintf(&sh, "echo 'BEGIN %s peer %s'\n%s serve %s %s %s - %s ADDR localhost %s - - %s 0 300 %d\necho \"END $?\"\n",
				mode, c.name, w(helper), w(l[0]), w(l[1]), dash(c.curves), w(peer), w(l[2]), m, n)
		}
	}
	out := linuxRunScript(t, dir, "tls12.sh", sh.String(), 6*time.Minute)
	sections := map[string][]string{}
	var cur string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "BEGIN "):
			cur = strings.TrimPrefix(line, "BEGIN ")
			sections[cur] = []string{}
		case strings.HasPrefix(line, "END "):
			if line != "END 0" {
				t.Errorf("%s: exit %s", cur, strings.TrimPrefix(line, "END "))
			}
			cur = ""
		case cur != "":
			sections[cur] = append(sections[cur], line)
		}
	}
	for _, mode := range tlsModes {
		got := sections[mode+" oracle"]
		if len(got) != len(oracle) {
			t.Errorf("%s oracle: %d answers for %d records", mode, len(got), len(oracle))
		} else {
			for i, c := range oracle {
				if got[i] != c.want {
					t.Errorf("%s oracle %s:\n got %.120s\nwant %.120s", mode, c.name, got[i], c.want)
				}
			}
		}
		for _, c := range peers {
			tlsLinuxCheck(t, mode+" peer "+c.name, sections[mode+" peer "+c.name], c.want, c.goWant)
		}
	}
}

// TestStdTls_Tls12OneByteRecords runs tlsOneByteRun against TLS 1.2 over
// AES-GCM and ChaCha20-Poly1305.
func TestStdTls_Tls12OneByteRecords(t *testing.T) {
	pki := tlsPeerPKI(t)
	for _, mode := range tlsModes {
		for _, s := range []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256} {
			t.Run(fmt.Sprintf("%s/%x", mode, s), func(t *testing.T) {
				cfg := &tls.Config{Certificates: []tls.Certificate{pki.leaves["p256"]}, MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{s}}
				tlsOneByteRun(t, pki, tls12Exe(t, "peer", mode), cfg)
			})
		}
	}
}
