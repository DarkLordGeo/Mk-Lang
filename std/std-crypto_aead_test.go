// Why: HMAC-SHA256, Equal, ChaCha20, Poly1305 and ChaCha20-Poly1305 must give
// the published answers on every input length.
// What: models checked on RFC 4231 and RFC 8439 vectors, the vectors in Mk,
// and Mk against the models on random inputs.
// How: Go models (Poly1305 by math/big), then seeded inputs of every length 0
// to 300 in dev and release, with every way Open must refuse.

package std

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/bits"
	"strings"
	"testing"
)

// ccModelBlock is RFC 8439 2.3's block function, written from the RFC.
func ccModelBlock(key, nonce []byte, ctr uint32) [64]byte {
	var s [16]uint32
	s[0], s[1], s[2], s[3] = 0x61707865, 0x3320646e, 0x79622d32, 0x6b206574
	for i := 0; i < 8; i++ {
		s[4+i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	s[12] = ctr
	for i := 0; i < 3; i++ {
		s[13+i] = binary.LittleEndian.Uint32(nonce[4*i:])
	}
	x := s
	qr := func(a, b, c, d int) {
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 16)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 12)
		x[a] += x[b]
		x[d] = bits.RotateLeft32(x[d]^x[a], 8)
		x[c] += x[d]
		x[b] = bits.RotateLeft32(x[b]^x[c], 7)
	}
	for r := 0; r < 10; r++ {
		qr(0, 4, 8, 12)
		qr(1, 5, 9, 13)
		qr(2, 6, 10, 14)
		qr(3, 7, 11, 15)
		qr(0, 5, 10, 15)
		qr(1, 6, 11, 12)
		qr(2, 7, 8, 13)
		qr(3, 4, 9, 14)
	}
	var out [64]byte
	for i := 0; i < 16; i++ {
		binary.LittleEndian.PutUint32(out[4*i:], x[i]+s[i])
	}
	return out
}

// ccModelXor is RFC 8439 2.4's encryption from counter ctr.
func ccModelXor(key, nonce []byte, ctr uint32, src []byte) []byte {
	out := make([]byte, len(src))
	for i := 0; i < len(src); i += 64 {
		ks := ccModelBlock(key, nonce, ctr+uint32(i/64))
		for j := i; j < len(src) && j < i+64; j++ {
			out[j] = src[j] ^ ks[j-i]
		}
	}
	return out
}

// polyModel is RFC 8439 2.5.1 by math/big: an independent method from
// the Mk code's limbs.
func polyModel(key, msg []byte) [16]byte {
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
	r := le(rb)
	s := le(key[16:32])
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 130), big.NewInt(5))
	acc := new(big.Int)
	for i := 0; i < len(msg); i += 16 {
		end := min(i+16, len(msg))
		blk := append(append([]byte(nil), msg[i:end]...), 1)
		acc.Add(acc, le(blk))
		acc.Mul(acc, r)
		acc.Mod(acc, p)
	}
	acc.Add(acc, s)
	var out [16]byte
	b := acc.Bytes() // big-endian
	for i := 0; i < 16 && i < len(b); i++ {
		out[i] = b[len(b)-1-i]
	}
	return out
}

// aeadModelSeal is RFC 8439 2.8's AEAD construction.
func aeadModelSeal(key, nonce, aad, plain []byte) []byte {
	blk := ccModelBlock(key, nonce, 0)
	ct := ccModelXor(key, nonce, 1, plain)
	pad := func(b []byte) []byte {
		if len(b)%16 == 0 {
			return b
		}
		return append(append([]byte(nil), b...), make([]byte, 16-len(b)%16)...)
	}
	mac := append(pad(aad), pad(ct)...)
	mac = binary.LittleEndian.AppendUint64(mac, uint64(len(aad)))
	mac = binary.LittleEndian.AppendUint64(mac, uint64(len(ct)))
	tag := polyModel(blk[:32], mac)
	return append(ct, tag[:]...)
}

var (
	rfcKey80    = "808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f"
	rfcKey00    = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	sunscreen   = "Ladies and Gentlemen of the class of '99: If I could offer you only one tip for the future, sunscreen would be it."
	rfc8439Aead = struct{ nonce, aad, want string }{
		"070000004041424344454647", "50515253c0c1c2c3c4c5c6c7",
		"d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc3ff4def08e4b7a9de576d26586cec64b61161ae10b594f09e26a7e902ecbd0600691",
	}
	// RFC 8439 2.3.2: the block for key 00..1f, nonce 000000090000004a00000000, counter 1.
	rfc8439Block = "10f1e7e4d13b5915500fdd1fa32071c4c7d1f4c733c068030422aa9ac3d46c4ed2826446079faa0914c2d705d98b02a2b5129cd1de164eb9cbd083e8a2503c4e"
	// RFC 8439 2.4.2: sunscreen encrypted under key 00..1f, nonce 000000000000004a00000000, counter 1.
	rfc8439Sunscreen = "6e2e359a2568f98041ba0728dd0d6981e97e7aec1d4360c20a27afccfd9fae0bf91b65c5524733ab8f593dabcd62b3571639d624e65152ab8f530c359f0861d807ca0dbf500d6a6156a38e088a22b65e52bc514d16ccf806818ce91ab77937365af90bbf74a35be6b40b8eedf2785e42874d"
	// RFC 8439 2.6.2: the one-time key for key 80..9f, nonce 000000000001020304050607.
	rfc8439Otk = "8ad5a08b905f81cc815040274ab29471a833b637e3fd0da508dbb8e2fdd1a646"
	// RFC 8439 2.5.2, and Zig 0.17.0's poly1305.zig "requiring a final reduction".
	polyVectors = []struct{ key, msg, tag string }{
		{"85d6be7857556d337f4452fe42d506a80103808afb0db2fd4abff6af4149f51b", hex.EncodeToString([]byte("Cryptographic Forum Research Group")), "a8061dc1305136c6c22b8baf0c0127a9"},
		{"be3f5f399b674daa07626a2c75ba5ab96d76b8184529a6f37784973d342b40fa", "fdc1f9924606d6e283d5f1741418d2e04197ff6885", "190df92aa439633c95b54a4a0d3f7906"},
	}
	// RFC 4231 sections 4.2 to 4.8 (case 5's tag is truncated to 128 bits).
	hmacVectors = []struct{ key, data, mac string }{
		{strings.Repeat("0b", 20), hex.EncodeToString([]byte("Hi There")), "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"},
		{hex.EncodeToString([]byte("Jefe")), hex.EncodeToString([]byte("what do ya want for nothing?")), "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
		{strings.Repeat("aa", 20), strings.Repeat("dd", 50), "773ea91e36800e46854db8ebd09181a72959098b3ef8c122d9635514ced565fe"},
		{"0102030405060708090a0b0c0d0e0f10111213141516171819", strings.Repeat("cd", 50), "82558a389a443c0ea4cc819899f2083a85f0faa3e578f8077a2e3ff46729665b"},
		{strings.Repeat("0c", 20), hex.EncodeToString([]byte("Test With Truncation")), "a3b6167473100ee06e0c796c2955552b"},
		{strings.Repeat("aa", 131), hex.EncodeToString([]byte("Test Using Larger Than Block-Size Key - Hash Key First")), "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54"},
		{strings.Repeat("aa", 131), hex.EncodeToString([]byte("This is a test using a larger than block-size key and a larger than block-size data. The key needs to be hashed before being used by the HMAC algorithm.")), "9b09ffa71b942fcb27635fbcd5b0e944bfdc63644f0713938a7f51535c3a35e2"},
	}
)

func goHmac(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// TestSTDCRYPTO_ModelsMatchPublishedVectors: the Go models (and Go's
// crypto/hmac) give the published values, so they can judge the Mk code.
func TestSTDCRYPTO_ModelsMatchPublishedVectors(t *testing.T) {
	for i, v := range hmacVectors {
		got := hex.EncodeToString(goHmac(cryptoUnhex(t, v.key), cryptoUnhex(t, v.data)))
		if !strings.HasPrefix(got, v.mac) {
			t.Errorf("RFC 4231 case %d: crypto/hmac %s, want %s", i+1, got, v.mac)
		}
	}
	blk := ccModelBlock(cryptoUnhex(t, rfcKey00), cryptoUnhex(t, "000000090000004a00000000"), 1)
	if got := hex.EncodeToString(blk[:]); got != rfc8439Block {
		t.Errorf("RFC 8439 2.3.2: model %s", got)
	}
	ss := ccModelXor(cryptoUnhex(t, rfcKey00), cryptoUnhex(t, "000000000000004a00000000"), 1, []byte(sunscreen))
	if got := hex.EncodeToString(ss); got != rfc8439Sunscreen {
		t.Errorf("RFC 8439 2.4.2: model %s", got)
	}
	otk := ccModelBlock(cryptoUnhex(t, rfcKey80), cryptoUnhex(t, "000000000001020304050607"), 0)
	if got := hex.EncodeToString(otk[:32]); got != rfc8439Otk {
		t.Errorf("RFC 8439 2.6.2: model %s", got)
	}
	for i, v := range polyVectors {
		tag := polyModel(cryptoUnhex(t, v.key), cryptoUnhex(t, v.msg))
		if got := hex.EncodeToString(tag[:]); got != v.tag {
			t.Errorf("Poly1305 vector %d: model %s, want %s", i, got, v.tag)
		}
	}
	sealed := aeadModelSeal(cryptoUnhex(t, rfcKey80), cryptoUnhex(t, rfc8439Aead.nonce), cryptoUnhex(t, rfc8439Aead.aad), []byte(sunscreen))
	if got := hex.EncodeToString(sealed); got != rfc8439Aead.want {
		t.Errorf("RFC 8439 2.8.2: model %s", got)
	}
	empty := aeadModelSeal(cryptoUnhex(t, rfcKey80), cryptoUnhex(t, rfc8439Aead.nonce), nil, nil)
	if got := hex.EncodeToString(empty); got != "a0784d7a4716f3feb4f64e7f4b39bf04" {
		t.Errorf("empty AEAD (Zig 0.17.0 chacha20.zig): model %s", got)
	}
}

// cryptoVectorProgram computes every published vector in Mk.
func cryptoVectorProgram() string {
	var b strings.Builder
	b.WriteString(cryptoPrelude)
	b.WriteString(`
Bytes(a: &Arena, s: str): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](s.Len() / 2)
    Unhex(s, mut out[..])
    return out[..]
}

OpenRes(key: &[32]u8, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): int {
    crypto.ChaCha20Poly1305.Open(key, nonce, aad, sealed, mut out[..]) else e {
        match e {
            crypto.BadTag {
                return 1
            }
            crypto.BadLength {
                return 2
            }
            _ {
                return 3
            }
        }
    }
    return 0
}

`)
	// One function per vector, called in order by Main.
	var bodies []string
	add := func(format string, args ...any) { bodies = append(bodies, fmt.Sprintf(format, args...)) }
	for _, v := range hmacVectors {
		add(`    k := try Bytes(a, %q)
    d := try Bytes(a, %q)
    h := crypto.Hmac256(k[..], d[..])
    try Print(out, "H ")
    try Hex(out, h[..])
    try Print(out, "\n")
`, v.key, v.data)
	}
	add(`    k := Arr32(try Bytes(a, %q))
    n := Arr12(try Bytes(a, %q))
    blk := crypto.FxChaBlock(&k, &n, 1)
    try Print(out, "B ")
    try Hex(out, blk[..])
    try Print(out, "\n")
    n2 := Arr12(try Bytes(a, %q))
    src := try Bytes(a, %q)
    dst := try a.Slice[u8](src.Len())
    crypto.FxChaXor(&k, &n2, 1, src, mut dst[..])
    try Print(out, "X ")
    try Hex(out, dst[..])
    try Print(out, "\n")
`, rfcKey00, "000000090000004a00000000", "000000000000004a00000000", hex.EncodeToString([]byte(sunscreen)))
	add(`    k := Arr32(try Bytes(a, %q))
    n := Arr12(try Bytes(a, %q))
    blk := crypto.FxChaBlock(&k, &n, 0)
    try Print(out, "K ")
    try Hex(out, blk[0..32])
    try Print(out, "\n")
`, rfcKey80, "000000000001020304050607")
	for _, v := range polyVectors {
		add(`    k := try Bytes(a, %q)
    m := try Bytes(a, %q)
    tag := crypto.FxPolyMac(k, m)
    try Print(out, "P ")
    try Hex(out, tag[..])
    try Print(out, "\n")
`, v.key, v.msg)
	}
	for _, aad := range []string{rfc8439Aead.aad, ""} {
		plain := hex.EncodeToString([]byte(sunscreen))
		if aad == "" {
			plain = ""
		}
		add(`    k := Arr32(try Bytes(a, %q))
    n := Arr12(try Bytes(a, %q))
    ad := try Bytes(a, %q)
    p := try Bytes(a, %q)
    s := try a.Slice[u8](p.Len() + 16)
    try crypto.ChaCha20Poly1305.Seal(&k, &n, ad, p, mut s[..])
    try Print(out, "S ")
    try Hex(out, s[..])
    back := try a.Slice[u8](p.Len())
    r := OpenRes(&k, &n, ad, s[..], mut back[..])
    same := crypto.Equal(back[..], p)
    try Print(out, " {r} {same}\n", (r: r, same: same))
`, rfcKey80, rfc8439Aead.nonce, aad, plain)
	}
	for i, body := range bodies {
		fmt.Fprintf(&b, "V%d(out: mut Writer, a: &Arena): WriteError|OutOfMemory|crypto.Error!void {\n%s}\n\n", i, body)
	}
	b.WriteString("export Main(sys: &System): WriteError|OutOfMemory|crypto.Error!void {\n    var out := sys.Stdout()\n")
	for i := range bodies {
		fmt.Fprintf(&b, "    try V%d(mut out, sys.arena)\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

func cryptoVectorWant(t *testing.T) string {
	var w strings.Builder
	for _, v := range hmacVectors {
		fmt.Fprintf(&w, "H %s\n", hex.EncodeToString(goHmac(cryptoUnhex(t, v.key), cryptoUnhex(t, v.data))))
	}
	fmt.Fprintf(&w, "B %s\nX %s\nK %s\n", rfc8439Block, rfc8439Sunscreen, rfc8439Otk)
	for _, v := range polyVectors {
		fmt.Fprintf(&w, "P %s\n", v.tag)
	}
	fmt.Fprintf(&w, "S %s 0 true\nS a0784d7a4716f3feb4f64e7f4b39bf04 0 true\n", rfc8439Aead.want)
	return w.String()
}

// TestSTDCRYPTO_PublishedVectorsInMk: every vector above, computed by
// std.crypto in a dev and a release build.
func TestSTDCRYPTO_PublishedVectorsInMk(t *testing.T) {
	src := cryptoVectorProgram()
	want := cryptoVectorWant(t)
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("vectors %v", m), cryptoRun(t, src, m...), want)
	}
}

// cryptoAeadMain: for every length 0 to 300 a seeded key, nonce, aad and
// plaintext; Seal, Open of the result, Open after flipping one bit of the
// ciphertext, of the tag and of the aad, and Open with each wrong length;
// HMAC under keys of 0 to 200 bytes; Equal on equal and unequal inputs.
const cryptoAeadMain = cryptoPrelude + `
OpenRes(key: &[32]u8, nonce: &[12]u8, aad: []u8, sealed: []u8, out: mut []u8): int {
    crypto.ChaCha20Poly1305.Open(key, nonce, aad, sealed, mut out[..]) else e {
        match e {
            crypto.BadTag {
                return 1
            }
            crypto.BadLength {
                return 2
            }
            _ {
                return 3
            }
        }
    }
    return 0
}

SealRes(key: &[32]u8, nonce: &[12]u8, aad: []u8, plain: []u8, out: mut []u8): int {
    crypto.ChaCha20Poly1305.Seal(key, nonce, aad, plain, mut out[..]) else e {
        match e {
            crypto.BadTag {
                return 1
            }
            crypto.BadLength {
                return 2
            }
            _ {
                return 3
            }
        }
    }
    return 0
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var r := Rng(s: 11)
    var kb: [32]u8 = [0; 32]
    var nb: [12]u8 = [0; 12]
    for n in 0..301 {
        Fill(mut r, mut kb[..])
        Fill(mut r, mut nb[..])
        alen := (n * 7) % 70
        ad := try sys.arena.Slice[u8](alen)
        Fill(mut r, mut ad[..])
        p := try sys.arena.Slice[u8](n)
        Fill(mut r, mut p[..])
        s := try sys.arena.Slice[u8](n + 16)
        sr := SealRes(&kb, &nb, ad[..], p[..], mut s[..])
        try Print(mut out, "S {n} {sr} ", (n: n, sr: sr))
        try Hex(mut out, s[..])
        back := try sys.arena.Slice[u8](n)
        o1 := OpenRes(&kb, &nb, ad[..], s[..], mut back[..])
        same := crypto.Equal(back[..], p[..])
        // One flipped bit in the ciphertext (when there is one), the tag
        // and the aad: each is BadTag and leaves back as Open found it.
        for i in 0..n {
            back[i] = 0x5A
        }
        var oc := -1
        if n > 0 {
            j := Trunc[int](r.Next() % Trunc[u64](n))
            fb := Trunc[u8](1) << Trunc[int](r.Next() % 8)
            s[j] = s[j] ^ fb
            oc = OpenRes(&kb, &nb, ad[..], s[..], mut back[..])
            s[j] = s[j] ^ fb
        }
        t := n + Trunc[int](r.Next() % 16)
        bit := Trunc[u8](1) << Trunc[int](r.Next() % 8)
        s[t] = s[t] ^ bit
        ot := OpenRes(&kb, &nb, ad[..], s[..], mut back[..])
        s[t] = s[t] ^ bit
        var oa := -1
        if alen > 0 {
            ad[0] = ad[0] ^ 0x80
            oa = OpenRes(&kb, &nb, ad[..], s[..], mut back[..])
            ad[0] = ad[0] ^ 0x80
        }
        var untouched := true
        for i in 0..n {
            if back[i] != 0x5A {
                untouched = false
            }
        }
        // Wrong lengths: out one byte short and one long, sealed too short.
        long := try sys.arena.Slice[u8](n + 1)
        ol := OpenRes(&kb, &nb, ad[..], s[..], mut long[..])
        sl := SealRes(&kb, &nb, ad[..], p[..], mut long[..])
        var os := -1
        if n > 0 {
            os = OpenRes(&kb, &nb, ad[..], s[..], mut back[0..n - 1])
        }
        oshort := OpenRes(&kb, &nb, ad[..], s[0..n % 16], mut back[0..0])
        try Print(mut out, " {o1} {same} {oc} {ot} {oa} {untouched} {ol} {sl} {os} {oshort}\n",
            (o1: o1, same: same, oc: oc, ot: ot, oa: oa, untouched: untouched, ol: ol, sl: sl, os: os, oshort: oshort))
    }
    for n in 0..201 {
        k := try sys.arena.Slice[u8](n)
        Fill(mut r, mut k[..])
        d := try sys.arena.Slice[u8]((n * 13) % 300)
        Fill(mut r, mut d[..])
        h := crypto.Hmac256(k[..], d[..])
        try Print(mut out, "H {n} ", (n: n))
        try Hex(mut out, h[..])
        try Print(mut out, "\n")
    }
    for n in 0..70 {
        x := try sys.arena.Slice[u8](n)
        Fill(mut r, mut x[..])
        y := try sys.arena.Slice[u8](n)
        for i in 0..n {
            y[i] = x[i]
        }
        e1 := crypto.Equal(x[..], y[..])
        var e2 := false
        if n > 0 {
            i := Trunc[int](r.Next() % Trunc[u64](n))
            y[i] = y[i] ^ (Trunc[u8](1) << Trunc[int](r.Next() % 8))
            e2 = crypto.Equal(x[..], y[..])
        }
        e3 := crypto.Equal(x[..], y[0..n / 2])
        try Print(mut out, "E {n} {e1} {e2} {e3}\n", (n: n, e1: e1, e2: e2, e3: e3))
    }
}
`

// cryptoAeadWant is the models' output for cryptoAeadMain.
func cryptoAeadWant() string {
	var w strings.Builder
	r := &cryptoSplitmix{s: 11}
	for n := 0; n <= 300; n++ {
		kb := make([]byte, 32)
		nb := make([]byte, 12)
		r.fill(kb)
		r.fill(nb)
		ad := make([]byte, (n*7)%70)
		r.fill(ad)
		p := make([]byte, n)
		r.fill(p)
		s := aeadModelSeal(kb, nb, ad, p)
		oc := -1
		if n > 0 {
			r.next() // the flipped byte
			r.next() // and bit
			oc = 1
		}
		r.next() // the tag byte
		r.next() // and bit
		oa := -1
		if len(ad) > 0 {
			oa = 1
		}
		os := -1
		if n > 0 {
			os = 2
		}
		// Result codes: 0 ok, 1 BadTag, 2 BadLength, -1 not run.
		fmt.Fprintf(&w, "S %d 0 %x 0 true %d 1 %d true 2 2 %d 2\n", n, s, oc, oa, os)
	}
	for n := 0; n <= 200; n++ {
		k := make([]byte, n)
		r.fill(k)
		d := make([]byte, (n*13)%300)
		r.fill(d)
		fmt.Fprintf(&w, "H %d %x\n", n, goHmac(k, d))
	}
	for n := 0; n < 70; n++ {
		x := make([]byte, n)
		r.fill(x)
		e2 := false
		if n > 0 {
			r.next()
			r.next()
		}
		fmt.Fprintf(&w, "E %d true %v %v\n", n, e2, n == 0)
	}
	return w.String()
}

// TestSTDCRYPTO_AeadHmacEqualMatchModels: Seal against the RFC 8439 model
// at every length 0 to 300, Open's round trip, BadTag for one flipped bit
// of ciphertext, tag or aad with out left as it was, BadLength for every
// wrong length; Hmac256 against crypto/hmac for keys of 0 to 200 bytes
// (across the 64-byte block and the hash-the-key edge); Equal on equal,
// one-bit-different and unequal-length inputs. Dev and release.
func TestSTDCRYPTO_AeadHmacEqualMatchModels(t *testing.T) {
	want := cryptoAeadWant()
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("aead %v", m), cryptoRun(t, cryptoAeadMain, m...), want)
	}
}
