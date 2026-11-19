// Why: tls.Server must refuse inputs Go's crypto/tls client never sends.
// What: the TestStdTls_ server tests: ChangeCipherSpec before the
// ClientHello, unaccepted early data, a misplaced pre_shared_key, a client
// ticket, oversized messages, every suite alone, and
// TestStdTls_ChaCha20Model.
// How: a TLS 1.3 client written here from Go's crypto primitives, with its own
// RFC 8439 ChaCha20-Poly1305 (tlsChaCha), checks the server's CertificateVerify
// and Finished itself.

package std

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"math/bits"
	"net"
	"testing"
	"time"
)

// tlsRawOpts bends the raw client's handshake one way.
type tlsRawOpts struct {
	suite    uint16 // the one suite offered
	ccsFirst bool   // a ChangeCipherSpec before the ClientHello
	early    int    // records of 100 bytes of early data after the ClientHello, with early_data and a pre_shared_key
	hrr      bool   // the first ClientHello has no key share, so the server asks for X25519 (HelloRetryRequest)
	pskFirst bool   // pre_shared_key, then server_name after it
	ticket   bool   // a NewSessionTicket under the client's application key before the data
	raw      []byte // sent as one handshake record instead of a ClientHello
	payload  int    // bytes echoed; 5 when 0
	// alertAfter: the server is expected to refuse the early data, so
	// the client sends nothing after it (no unread bytes, so the server's
	// close is no reset) and reads the alert after the server's flight,
	// or after the HelloRetryRequest.
	alertAfter bool
}

// tlsRawConn is the raw client's record layer.
type tlsRawConn struct {
	c net.Conn
}

func (r *tlsRawConn) write(ty byte, body []byte) error {
	rec := append([]byte{ty, 3, 3, byte(len(body) >> 8), byte(len(body))}, body...)
	_, err := r.c.Write(rec)
	return err
}

func (r *tlsRawConn) read() (byte, []byte, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(r.c, head); err != nil {
		return 0, nil, err
	}
	body := make([]byte, int(head[3])<<8|int(head[4]))
	if _, err := io.ReadFull(r.c, body); err != nil {
		return 0, nil, err
	}
	return head[0], body, nil
}

// tlsRawKey is one direction's protection.
type tlsRawKey struct {
	aead cipher.AEAD
	iv   []byte
	seq  uint64
}

func (k *tlsRawKey) nonce() []byte {
	n := append([]byte(nil), k.iv...)
	for i := 0; i < 8; i++ {
		n[len(n)-1-i] ^= byte(k.seq >> (8 * i))
	}
	k.seq++
	return n
}

func (k *tlsRawKey) seal(ty byte, data []byte) []byte {
	inner := append(append([]byte(nil), data...), ty)
	n := len(inner) + k.aead.Overhead()
	head := []byte{23, 3, 3, byte(n >> 8), byte(n)}
	return append(head, k.aead.Seal(nil, k.nonce(), inner, head)...)
}

func (k *tlsRawKey) open(body []byte) (byte, []byte, error) {
	head := []byte{23, 3, 3, byte(len(body) >> 8), byte(len(body))}
	p, err := k.aead.Open(nil, k.nonce(), body, head)
	if err != nil {
		return 0, nil, err
	}
	i := len(p)
	for i > 0 && p[i-1] == 0 {
		i--
	}
	if i == 0 {
		return 0, nil, errors.New("all-zero inner plaintext")
	}
	return p[i-1], p[:i-1], nil
}

// tlsRawSuite is a suite's hash and AEAD key length; ok false for a suite
// the raw client cannot run (ChaCha20-Poly1305).
func tlsRawSuite(s uint16) (func() hash.Hash, int, bool) {
	switch s {
	case 0x1301:
		return sha256.New, 16, true
	case 0x1302:
		return sha512.New384, 32, true
	case 0x1303:
		return sha256.New, 32, true
	}
	return nil, 0, false
}

func tlsRawExpand(h func() hash.Hash, secret []byte, label string, ctx []byte, n int) []byte {
	full := "tls13 " + label
	info := []byte{byte(n >> 8), byte(n), byte(len(full))}
	info = append(info, full...)
	info = append(info, byte(len(ctx)))
	info = append(info, ctx...)
	out, err := hkdf.Expand(h, secret, string(info), n)
	if err != nil {
		panic(err)
	}
	return out
}

func tlsRawSum(h func() hash.Hash, b []byte) []byte {
	x := h()
	x.Write(b)
	return x.Sum(nil)
}

func tlsRawKeyOf(h func() hash.Hash, keyLen int, secret []byte, chacha bool) *tlsRawKey {
	key := tlsRawExpand(h, secret, "key", nil, keyLen)
	iv := tlsRawExpand(h, secret, "iv", nil, 12)
	if chacha {
		return &tlsRawKey{aead: tlsChaCha(key), iv: iv}
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		panic(err)
	}
	return &tlsRawKey{aead: g, iv: iv}
}

func tlsRawFinished(h func() hash.Hash, base, transcript []byte) []byte {
	k := tlsRawExpand(h, base, "finished", nil, h().Size())
	m := hmac.New(h, k)
	m.Write(tlsRawSum(h, transcript))
	return m.Sum(nil)
}

func tlsRawU16(b []byte, v uint16) []byte { return append(b, byte(v>>8), byte(v)) }

func tlsRawExt(b []byte, ty uint16, data []byte) []byte {
	b = tlsRawU16(b, ty)
	b = tlsRawU16(b, uint16(len(data)))
	return append(b, data...)
}

func tlsRawMsg(ty byte, body []byte) []byte {
	return append([]byte{ty, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
}

// tlsRawHello is a ClientHello offering o.suite; share is the X25519
// public value, nil for an empty key_share; early adds early_data and a
// pre_shared_key with random identity and binder.
func tlsRawHello(o tlsRawOpts, random, sid, share []byte, early bool) []byte {
	var b []byte
	b = tlsRawU16(b, 0x0303)
	b = append(b, random...)
	b = append(b, byte(len(sid)))
	b = append(b, sid...)
	b = tlsRawU16(b, 2)
	b = tlsRawU16(b, o.suite)
	b = append(b, 1, 0)
	var e []byte
	if !o.pskFirst {
		name := []byte("localhost")
		sni := tlsRawU16(nil, uint16(len(name)+3))
		sni = append(sni, 0)
		sni = tlsRawU16(sni, uint16(len(name)))
		e = tlsRawExt(e, 0, append(sni, name...))
	}
	e = tlsRawExt(e, 43, []byte{2, 3, 4})
	e = tlsRawExt(e, 10, []byte{0, 2, 0, 0x1d})
	e = tlsRawExt(e, 13, []byte{0, 6, 4, 3, 8, 7, 8, 4})
	ks := []byte{}
	if share != nil {
		ks = append(ks, 0, 0x1d)
		ks = tlsRawU16(ks, uint16(len(share)))
		ks = append(ks, share...)
	}
	e = tlsRawExt(e, 51, append(tlsRawU16(nil, uint16(len(ks))), ks...))
	if early || o.pskFirst {
		if early {
			e = tlsRawExt(e, 42, nil)
		}
		e = tlsRawExt(e, 45, []byte{1, 1})
		id := make([]byte, 32)
		rand.Read(id)
		ids := tlsRawU16(nil, 32)
		ids = append(ids, id...)
		ids = append(ids, 0, 0, 0, 7)
		binder := make([]byte, 32)
		rand.Read(binder)
		psk := tlsRawU16(nil, uint16(len(ids)))
		psk = append(psk, ids...)
		psk = tlsRawU16(psk, 33)
		psk = append(psk, 32)
		psk = append(psk, binder...)
		e = tlsRawExt(e, 41, psk)
	}
	if o.pskFirst {
		name := []byte("localhost")
		sni := tlsRawU16(nil, uint16(len(name)+3))
		sni = append(sni, 0)
		sni = tlsRawU16(sni, uint16(len(name)))
		e = tlsRawExt(e, 0, append(sni, name...))
	}
	b = tlsRawU16(b, uint16(len(e)))
	b = append(b, e...)
	return tlsRawMsg(1, b)
}

// tlsRawHrrRandom is RFC 8446 4.1.3's HelloRetryRequest random.
var tlsRawHrrRandom = []byte{0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
	0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c}

// tlsRawServerHello reads a ServerHello's suite, whether it is a
// HelloRetryRequest, and its X25519 share.
func tlsRawServerHello(m []byte) (suite uint16, retry bool, key []byte, err error) {
	if len(m) < 4 || m[0] != 2 {
		return 0, false, nil, fmt.Errorf("not a ServerHello: %x", m[:min(len(m), 8)])
	}
	b := m[4:]
	if len(b) < 35 {
		return 0, false, nil, errors.New("short ServerHello")
	}
	retry = bytes.Equal(b[2:34], tlsRawHrrRandom)
	b = b[34:]
	b = b[1+int(b[0]):]
	suite = binary.BigEndian.Uint16(b)
	b = b[3:]
	n := int(binary.BigEndian.Uint16(b))
	b = b[2 : 2+n]
	for len(b) >= 4 {
		ty := binary.BigEndian.Uint16(b)
		l := int(binary.BigEndian.Uint16(b[2:]))
		d := b[4 : 4+l]
		if ty == 51 && !retry && len(d) >= 4 {
			key = d[4 : 4+int(binary.BigEndian.Uint16(d[2:]))]
		}
		b = b[4+l:]
	}
	return suite, retry, key, nil
}

// tlsRawVerify checks the server's CertificateVerify over th (the
// transcript hash through Certificate) with the leaf of cert (a
// Certificate message).
func tlsRawVerify(cert, cv, th []byte) error {
	b := cert[4:]
	b = b[1+int(b[0]):]
	b = b[3:]
	n := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	leaf, err := x509.ParseCertificate(b[3 : 3+n])
	if err != nil {
		return err
	}
	c := cv[4:]
	scheme := binary.BigEndian.Uint16(c)
	sig := c[4 : 4+int(binary.BigEndian.Uint16(c[2:]))]
	content := append(bytes.Repeat([]byte{0x20}, 64), "TLS 1.3, server CertificateVerify"...)
	content = append(content, 0)
	content = append(content, th...)
	switch scheme {
	case 0x0403:
		d := sha256.Sum256(content)
		if !ecdsa.VerifyASN1(leaf.PublicKey.(*ecdsa.PublicKey), d[:], sig) {
			return errors.New("bad ECDSA CertificateVerify")
		}
	case 0x0807:
		if !ed25519.Verify(leaf.PublicKey.(ed25519.PublicKey), content, sig) {
			return errors.New("bad Ed25519 CertificateVerify")
		}
	default:
		return fmt.Errorf("scheme %04x", scheme)
	}
	return nil
}

// tlsRawAlert reads the server's answer when it refuses: "alert <code>"
// for a plaintext alert, "serverhello" for a ServerHello, else what came.
func tlsRawAlert(rc *tlsRawConn) string {
	ty, body, err := rc.read()
	if err != nil {
		return "err " + err.Error()
	}
	if ty == 21 && len(body) == 2 {
		return fmt.Sprintf("alert %d", body[1])
	}
	if ty == 22 && len(body) > 0 && body[0] == 2 {
		return "serverhello"
	}
	return fmt.Sprintf("record %d %x", ty, body[:min(len(body), 8)])
}

// tlsRawOpenAlert reads one protected record under k and reports the
// alert it holds.
func tlsRawOpenAlert(rc *tlsRawConn, k *tlsRawKey) string {
	ty, body, err := rc.read()
	if err != nil {
		return "err " + err.Error()
	}
	if ty != 23 {
		return fmt.Sprintf("err record %d", ty)
	}
	inner, p, err := k.open(body)
	if err != nil {
		return "err " + err.Error()
	}
	if inner != 21 || len(p) != 2 {
		return fmt.Sprintf("err inner type %d", inner)
	}
	return fmt.Sprintf("alert %d", p[1])
}

// tlsRawClient runs the raw client against addr and returns "ok suite=..
// n=..", "alert <code>" (the server's alert, plaintext or opened), or
// "err ...".
func tlsRawClient(t *testing.T, addr string, o tlsRawOpts) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return "err " + err.Error()
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	rc := &tlsRawConn{c: c}
	if o.raw != nil {
		rc.write(22, o.raw)
		return tlsRawAlert(rc)
	}
	h, keyLen, ok := tlsRawSuite(o.suite)
	if !ok {
		return "err suite not built here"
	}
	cc := o.suite == 0x1303
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "err " + err.Error()
	}
	random := make([]byte, 32)
	sid := make([]byte, 32)
	rand.Read(random)
	rand.Read(sid)
	if o.ccsFirst {
		// The server refuses at this record: nothing more is sent, so it
		// closes with no unread byte (no reset).
		rc.write(20, []byte{1})
		return tlsRawAlert(rc)
	}
	var share []byte
	if !o.hrr {
		share = priv.PublicKey().Bytes()
	}
	ch := tlsRawHello(o, random, sid, share, o.early > 0)
	rc.write(22, ch)
	ccsSent := false
	if o.early > 0 {
		rc.write(20, []byte{1})
		ccsSent = true
		junk := make([]byte, 100)
		for i := 0; i < o.early; i++ {
			rand.Read(junk)
			rc.write(23, junk)
		}
	}
	if o.pskFirst {
		return tlsRawAlert(rc)
	}
	transcript := append([]byte(nil), ch...)
	// ServerHello, after a HelloRetryRequest when asked.
	var sh []byte
	for {
		ty, body, err := rc.read()
		if err != nil {
			return "err " + err.Error()
		}
		if ty == 20 {
			continue
		}
		if ty == 21 {
			return fmt.Sprintf("alert %d", body[len(body)-1])
		}
		if ty != 22 {
			return fmt.Sprintf("err record %d before ServerHello", ty)
		}
		suite, retry, key, err := tlsRawServerHello(body)
		if err != nil {
			return "err " + err.Error()
		}
		if suite != o.suite {
			return fmt.Sprintf("err suite %04x", suite)
		}
		if retry && o.alertAfter {
			for {
				ty, body, err := rc.read()
				if err != nil {
					return "err " + err.Error()
				}
				if ty == 21 && len(body) == 2 {
					return fmt.Sprintf("alert %d", body[1])
				}
				if ty != 20 {
					return fmt.Sprintf("err record %d after the HelloRetryRequest", ty)
				}
			}
		}
		if retry {
			// RFC 8446 4.4.1: the first ClientHello's hash as message_hash.
			first := tlsRawSum(h, transcript)
			transcript = append([]byte{254, 0, 0, byte(len(first))}, first...)
			transcript = append(transcript, body...)
			o2 := o
			o2.early = 0
			ch2 := tlsRawHello(o2, random, sid, priv.PublicKey().Bytes(), false)
			if !ccsSent {
				rc.write(20, []byte{1})
				ccsSent = true
			}
			rc.write(22, ch2)
			transcript = append(transcript, ch2...)
			continue
		}
		pub, err := ecdh.X25519().NewPublicKey(key)
		if err != nil {
			return "err " + err.Error()
		}
		shared, err := priv.ECDH(pub)
		if err != nil {
			return "err " + err.Error()
		}
		sh = body
		transcript = append(transcript, sh...)
		// The key schedule (RFC 8446 7.1).
		hl := h().Size()
		zero := make([]byte, hl)
		early, _ := hkdf.Extract(h, zero, zero)
		derived := tlsRawExpand(h, early, "derived", tlsRawSum(h, nil), hl)
		hsSecret, _ := hkdf.Extract(h, shared, derived)
		thHs := tlsRawSum(h, transcript)
		cHs := tlsRawExpand(h, hsSecret, "c hs traffic", thHs, hl)
		sHs := tlsRawExpand(h, hsSecret, "s hs traffic", thHs, hl)
		derived2 := tlsRawExpand(h, hsSecret, "derived", tlsRawSum(h, nil), hl)
		master, _ := hkdf.Extract(h, zero, derived2)
		rk := tlsRawKeyOf(h, keyLen, sHs, cc)
		// The server's encrypted flight.
		var msgs []byte
		var cert, cv []byte
		var thCert []byte
		for done := false; !done; {
			ty, body, err := rc.read()
			if err != nil {
				return "err " + err.Error()
			}
			if ty == 20 {
				continue
			}
			if ty != 23 {
				return fmt.Sprintf("err record %d in the server's flight", ty)
			}
			inner, p, err := rk.open(body)
			if err != nil {
				return "err " + err.Error()
			}
			if inner == 21 {
				return fmt.Sprintf("alert %d", p[len(p)-1])
			}
			msgs = append(msgs, p...)
			for len(msgs) >= 4 {
				n := 4 + (int(msgs[1])<<16 | int(msgs[2])<<8 | int(msgs[3]))
				if len(msgs) < n {
					break
				}
				m := msgs[:n]
				switch m[0] {
				case 8:
				case 11:
					cert = append([]byte(nil), m...)
				case 15:
					thCert = tlsRawSum(h, transcript)
					cv = append([]byte(nil), m...)
					if err := tlsRawVerify(cert, cv, thCert); err != nil {
						return "err " + err.Error()
					}
				case 20:
					want := tlsRawFinished(h, sHs, transcript)
					if !hmac.Equal(m[4:], want) {
						return "err the server's Finished does not verify"
					}
					done = true
				default:
					return fmt.Sprintf("err message %d in the server's flight", m[0])
				}
				transcript = append(transcript, m...)
				msgs = msgs[n:]
			}
		}
		thAp := tlsRawSum(h, transcript)
		cAp := tlsRawExpand(h, master, "c ap traffic", thAp, hl)
		sAp := tlsRawExpand(h, master, "s ap traffic", thAp, hl)
		if o.alertAfter {
			// The server writes under its application key once its flight
			// is out.
			return tlsRawOpenAlert(rc, tlsRawKeyOf(h, keyLen, sAp, cc))
		}
		// The client's second flight.
		if !ccsSent {
			rc.write(20, []byte{1})
		}
		wk := tlsRawKeyOf(h, keyLen, cHs, cc)
		fin := tlsRawMsg(20, tlsRawFinished(h, cHs, transcript))
		c.Write(wk.seal(22, fin))
		wk = tlsRawKeyOf(h, keyLen, cAp, cc)
		rk = tlsRawKeyOf(h, keyLen, sAp, cc)
		if o.ticket {
			// lifetime, age_add, an empty nonce, a 1-byte ticket, no
			// extensions: 14 bytes.
			nst := tlsRawMsg(4, []byte{0, 0, 0x0e, 0x10, 1, 2, 3, 4, 0, 0, 1, 0x55, 0, 0})
			c.Write(wk.seal(22, nst))
			return tlsRawOpenAlert(rc, rk)
		}
		n := o.payload
		if n == 0 {
			n = 5
		}
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i*13 + 5)
		}
		werr := make(chan error, 1)
		go func() {
			for at := 0; at < n; at += 16384 {
				if _, err := c.Write(wk.seal(23, data[at:min(n, at+16384)])); err != nil {
					werr <- err
					return
				}
			}
			werr <- nil
		}()
		var back []byte
		for len(back) < n {
			ty, body, err := rc.read()
			if err != nil {
				return "err " + err.Error()
			}
			if ty != 23 {
				return fmt.Sprintf("err record %d after the handshake", ty)
			}
			inner, p, err := rk.open(body)
			if err != nil {
				return "err " + err.Error()
			}
			if inner == 21 {
				return fmt.Sprintf("alert %d", p[len(p)-1])
			}
			if inner == 23 {
				back = append(back, p...)
			}
		}
		if err := <-werr; err != nil {
			return "err " + err.Error()
		}
		if !bytes.Equal(back, data) {
			return "err the echo differs"
		}
		c.Write(wk.seal(21, []byte{1, 0}))
		for {
			ty, body, err := rc.read()
			if err != nil {
				return "err no close_notify: " + err.Error()
			}
			if ty != 23 {
				return fmt.Sprintf("err record %d at the end", ty)
			}
			inner, p, err := rk.open(body)
			if err != nil {
				return "err " + err.Error()
			}
			if inner == 21 && len(p) == 2 && p[1] == 0 {
				break
			}
		}
		return fmt.Sprintf("ok suite=%04x n=%d", o.suite, n)
	}
}

// tlsRawCase is one raw client run against a fresh Mk server: the raw
// client's answer and the server's line.
type tlsRawCase struct {
	name   string
	opts   tlsRawOpts
	want   string
	server string
}

func tlsRunRaw(t *testing.T, cases []tlsRawCase) {
	t.Helper()
	pki := tlsPeerPKI(t)
	for _, mode := range tlsModes {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				addr, wait := tlsServeOnce(t, mode, pki.leaves["p256"], "-")
				got := tlsRawClient(t, addr, c.opts)
				line := wait()
				if got != c.want {
					t.Errorf("raw client: %q, want %q (server: %s)", got, c.want, line)
				}
				if line != c.server {
					t.Errorf("server: %q, want %q", line, c.server)
				}
			})
		}
	}
}

// TestStdTls_ServerSuites: a full handshake with tls.Server over
// TLS_AES_128_GCM_SHA256, TLS_AES_256_GCM_SHA384 and
// TLS_CHACHA20_POLY1305_SHA256, each offered alone,
// the server's CertificateVerify and Finished checked with Go's
// primitives and 1 MiB echoed.
func TestStdTls_ServerSuites(t *testing.T) {
	tlsRunRaw(t, []tlsRawCase{
		{name: "aes128gcm", opts: tlsRawOpts{suite: 0x1301}, want: "ok suite=1301 n=5", server: "served alpn=- n=5"},
		{name: "aes256gcm", opts: tlsRawOpts{suite: 0x1302}, want: "ok suite=1302 n=5", server: "served alpn=- n=5"},
		{name: "aes256gcm one MiB", opts: tlsRawOpts{suite: 0x1302, payload: 1 << 20}, want: "ok suite=1302 n=1048576", server: "served alpn=- n=1048576"},
		{name: "aes256gcm after HelloRetryRequest", opts: tlsRawOpts{suite: 0x1302, hrr: true}, want: "ok suite=1302 n=5", server: "served alpn=- n=5"},
		{name: "chacha20poly1305", opts: tlsRawOpts{suite: 0x1303}, want: "ok suite=1303 n=5", server: "served alpn=- n=5"},
		{name: "chacha20poly1305 one MiB", opts: tlsRawOpts{suite: 0x1303, payload: 1 << 20}, want: "ok suite=1303 n=1048576", server: "served alpn=- n=1048576"},
	})
}

// TestStdTls_ServerRefusesClientTicket: a NewSessionTicket from the
// client after the handshake is unexpected_message (RFC 8446 4.6.1); the
// server's Read fails with Protocol and the data after it is not echoed.
func TestStdTls_ServerRefusesClientTicket(t *testing.T) {
	tlsRunRaw(t, []tlsRawCase{
		{name: "client NewSessionTicket", opts: tlsRawOpts{suite: 0x1301, ticket: true}, want: "alert 10", server: "err Protocol"},
	})
}

// TestStdTls_EarlyDataSkipped: early data the server did not accept is
// skipped (RFC 8446 4.2.10): after the ServerHello by records that fail
// to open, before a second ClientHello by type; past MaxEarlySkip
// (16,384 bytes of records) the next one fails (bad_record_mac, or
// unexpected_message before the keys).
func TestStdTls_EarlyDataSkipped(t *testing.T) {
	tlsRunRaw(t, []tlsRawCase{
		{name: "two records skipped", opts: tlsRawOpts{suite: 0x1301, early: 2}, want: "ok suite=1301 n=5", server: "served alpn=- n=5"},
		{name: "156 records, 16380 bytes, skipped", opts: tlsRawOpts{suite: 0x1302, early: 156}, want: "ok suite=1302 n=5", server: "served alpn=- n=5"},
		{name: "157 records past the bound", opts: tlsRawOpts{suite: 0x1301, early: 157, alertAfter: true}, want: "alert 20", server: "err Protocol"},
		{name: "skipped before a second ClientHello", opts: tlsRawOpts{suite: 0x1301, early: 3, hrr: true}, want: "ok suite=1301 n=5", server: "served alpn=- n=5"},
		{name: "past the bound before a second ClientHello", opts: tlsRawOpts{suite: 0x1301, early: 157, hrr: true, alertAfter: true}, want: "alert 10", server: "err Protocol"},
	})
}

// TestStdTls_PskNotLastRefused: a ClientHello whose pre_shared_key is
// not the last extension is illegal_parameter (RFC 8446 4.2.11), though
// the server takes no PSK.
func TestStdTls_PskNotLastRefused(t *testing.T) {
	tlsRunRaw(t, []tlsRawCase{
		{name: "pre_shared_key before server_name", opts: tlsRawOpts{suite: 0x1301, pskFirst: true}, want: "alert 47", server: "err Protocol"},
	})
}

// TestStdTls_CcsBeforeHelloRefused: a ChangeCipherSpec before the first
// ClientHello is an unexpected record (RFC 8446 5).
func TestStdTls_CcsBeforeHelloRefused(t *testing.T) {
	tlsRunRaw(t, []tlsRawCase{
		{name: "ChangeCipherSpec first", opts: tlsRawOpts{suite: 0x1301, ccsFirst: true}, want: "alert 10", server: "err Protocol"},
	})
}

// TestStdTls_ServerBoundsMessages: the server reads no Certificate, so a
// message header of type 11 with a 256 KiB length is refused as soon as
// it is read (decode_error: over the 64 KiB bound), and a message of a
// type it does not expect at its header (unexpected_message), each after
// one 16 KiB record and without waiting for the rest.
func TestStdTls_ServerBoundsMessages(t *testing.T) {
	cert := append([]byte{11, 4, 0, 0}, make([]byte, 16380)...)
	other := append([]byte{2, 0, 0xff, 0}, make([]byte, 16380)...)
	tlsRunRaw(t, []tlsRawCase{
		{name: "256 KiB Certificate header", opts: tlsRawOpts{raw: cert}, want: "alert 50", server: "err Protocol"},
		{name: "ServerHello header to a server", opts: tlsRawOpts{raw: other}, want: "alert 10", server: "err Protocol"},
	})
}

// tlsChaChaAead is RFC 8439 2.8's AEAD_CHACHA20_POLY1305, written from
// the RFC for the raw client: ChaCha20 (2.3, 2.4) and Poly1305 (2.5) over
// math/big. Slow, and only a test's oracle. It is here because Go's
// standard library has no ChaCha20-Poly1305, and Go's crypto/tls client
// offers that suite first only without AES hardware (GODEBUG=cpu.aes=off is
// read too late on Windows to change that).
type tlsChaChaAead struct{ key []byte }

func tlsChaCha(key []byte) cipher.AEAD { return &tlsChaChaAead{key: append([]byte(nil), key...)} }

func (*tlsChaChaAead) NonceSize() int { return 12 }
func (*tlsChaChaAead) Overhead() int  { return 16 }

func tlsQuarter(x *[16]uint32, a, b, c, d int) {
	x[a] += x[b]
	x[d] = bits.RotateLeft32(x[d]^x[a], 16)
	x[c] += x[d]
	x[b] = bits.RotateLeft32(x[b]^x[c], 12)
	x[a] += x[b]
	x[d] = bits.RotateLeft32(x[d]^x[a], 8)
	x[c] += x[d]
	x[b] = bits.RotateLeft32(x[b]^x[c], 7)
}

// tlsChaChaBlock is RFC 8439 2.3's block function.
func tlsChaChaBlock(key []byte, counter uint32, nonce []byte) []byte {
	var s [16]uint32
	s[0], s[1], s[2], s[3] = 0x61707865, 0x3320646e, 0x79622d32, 0x6b206574
	for i := 0; i < 8; i++ {
		s[4+i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	s[12] = counter
	for i := 0; i < 3; i++ {
		s[13+i] = binary.LittleEndian.Uint32(nonce[4*i:])
	}
	x := s
	for i := 0; i < 10; i++ {
		tlsQuarter(&x, 0, 4, 8, 12)
		tlsQuarter(&x, 1, 5, 9, 13)
		tlsQuarter(&x, 2, 6, 10, 14)
		tlsQuarter(&x, 3, 7, 11, 15)
		tlsQuarter(&x, 0, 5, 10, 15)
		tlsQuarter(&x, 1, 6, 11, 12)
		tlsQuarter(&x, 2, 7, 8, 13)
		tlsQuarter(&x, 3, 4, 9, 14)
	}
	out := make([]byte, 64)
	for i := range x {
		binary.LittleEndian.PutUint32(out[4*i:], x[i]+s[i])
	}
	return out
}

// tlsChaChaXor is RFC 8439 2.4's encryption from counter.
func tlsChaChaXor(key []byte, counter uint32, nonce, in []byte) []byte {
	out := make([]byte, len(in))
	for at := 0; at < len(in); at += 64 {
		ks := tlsChaChaBlock(key, counter, nonce)
		counter++
		for i := at; i < len(in) && i < at+64; i++ {
			out[i] = in[i] ^ ks[i-at]
		}
	}
	return out
}

// tlsPoly1305 is RFC 8439 2.5's MAC of msg under the 32-byte key.
func tlsPoly1305(key, msg []byte) []byte {
	le := func(b []byte) *big.Int {
		r := make([]byte, len(b))
		for i := range b {
			r[len(b)-1-i] = b[i]
		}
		return new(big.Int).SetBytes(r)
	}
	rb := append([]byte(nil), key[:16]...)
	for _, i := range []int{3, 7, 11, 15} {
		rb[i] &= 15
	}
	for _, i := range []int{4, 8, 12} {
		rb[i] &= 252
	}
	r, sk := le(rb), le(key[16:32])
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	acc := new(big.Int)
	for at := 0; at < len(msg); at += 16 {
		blk := append(append([]byte(nil), msg[at:min(len(msg), at+16)]...), 1)
		acc.Add(acc, le(blk))
		acc.Mul(acc, r)
		acc.Mod(acc, p)
	}
	acc.Add(acc, sk)
	b := acc.Bytes()
	out := make([]byte, 16)
	for i := 0; i < 16 && i < len(b); i++ {
		out[i] = b[len(b)-1-i]
	}
	return out
}

func (c *tlsChaChaAead) tag(nonce, ct, ad []byte) []byte {
	otk := tlsChaChaBlock(c.key, 0, nonce)[:32]
	pad := func(b []byte) []byte { return append(b, make([]byte, (16-len(b)%16)%16)...) }
	m := pad(append([]byte(nil), ad...))
	m = pad(append(m, ct...))
	m = binary.LittleEndian.AppendUint64(m, uint64(len(ad)))
	m = binary.LittleEndian.AppendUint64(m, uint64(len(ct)))
	return tlsPoly1305(otk, m)
}

func (c *tlsChaChaAead) Seal(dst, nonce, plain, ad []byte) []byte {
	ct := tlsChaChaXor(c.key, 1, nonce, plain)
	return append(append(dst, ct...), c.tag(nonce, ct, ad)...)
}

func (c *tlsChaChaAead) Open(dst, nonce, sealed, ad []byte) ([]byte, error) {
	if len(sealed) < 16 {
		return nil, errors.New("short")
	}
	ct := sealed[:len(sealed)-16]
	if !hmac.Equal(c.tag(nonce, ct, ad), sealed[len(sealed)-16:]) {
		return nil, errors.New("chacha20poly1305: message authentication failed")
	}
	return append(dst, tlsChaChaXor(c.key, 1, nonce, ct)...), nil
}

// TestStdTls_ChaCha20Model checks tlsChaCha against RFC 8439's
// vectors: the Poly1305 MAC of 2.5.2 and the AEAD of 2.8.2.
func TestStdTls_ChaCha20Model(t *testing.T) {
	un := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mac := tlsPoly1305(un("85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b"), []byte("Cryptographic Forum Research Group"))
	if !bytes.Equal(mac, un("a8061dc1305136c6c22b8baf0c0127a9")) {
		t.Errorf("Poly1305: %x", mac)
	}
	key := un("808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f")
	nonce := un("070000004041424344454647")
	ad := un("50515253c0c1c2c3c4c5c6c7")
	plain := []byte("Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it.")
	sealed := tlsChaCha(key).Seal(nil, nonce, plain, ad)
	want := un("d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b6116" +
		"1ae10b594f09e26a7e902ecbd0600691")
	if !bytes.Equal(sealed, want) {
		t.Errorf("AEAD:\n got %x\nwant %x", sealed, want)
	}
	back, err := tlsChaCha(key).Open(nil, nonce, sealed, ad)
	if err != nil || !bytes.Equal(back, plain) {
		t.Errorf("Open: %v", err)
	}
	sealed[3] ^= 1
	if _, err := tlsChaCha(key).Open(nil, nonce, sealed, ad); err == nil {
		t.Error("a forged byte opened")
	}
}
