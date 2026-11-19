// Why: regression tests for bugs found in std.crypto, each pinned by a test
// named after it.
// What: Verify's acceptance set against Go's, RFC 8439 Appendix A.3 to A.5,
// and the AEAD length limit with ChaXor's last counter.
// How: signatures built to tell the verification rules apart; Go's
// cofactorless acceptance set is the one std.crypto documents.

package std

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// rfc8439A3 is RFC 8439 Appendix A.3, Poly1305 test vectors 1 to 11 (the
// key is r || s). Copied from ring 0.17.14's src/aead/poly1305_test.txt
// (from OpenSSL) and checked byte for byte against the RFC's text
// (rfc-editor.org/rfc/rfc8439.txt, A.3); polyModel computes
// every tag (TestSTDCRYPTO_RFC8439AppendixA). #5 to #11 are the
// carry and final-reduction edges: h in [p, 2^130), an overflowing s,
// limb carries, h exactly p, a 131-bit intermediate.
var rfc8439A3 = []struct{ key, msg, tag string }{
	{"0000000000000000000000000000000000000000000000000000000000000000", strings.Repeat("00", 64), "00000000000000000000000000000000"},
	{"0000000000000000000000000000000036e5f6b5c5e06070f0efca96227a863e", rfcIetfText, "36e5f6b5c5e06070f0efca96227a863e"},
	{"36e5f6b5c5e06070f0efca96227a863e00000000000000000000000000000000", rfcIetfText, "f3477e7cd95417af89a6b8794c310cf0"},
	{"1c9240a5eb55d38af333888604f6b5f0473917c1402b80099dca5cbc207075c0",
		"2754776173206272696c6c69672c20616e642074686520736c6974687920746f7665730a446964206779726520616e642067696d626c6520696e2074686520776162653a0a416c6c206d696d737920776572652074686520626f726f676f7665732c0a416e6420746865206d6f6d65207261746873206f757467726162652e",
		"4541669a7eaaee61e708dc7cbcc5eb62"},
	{"0200000000000000000000000000000000000000000000000000000000000000", "ffffffffffffffffffffffffffffffff", "03000000000000000000000000000000"},
	{"02000000000000000000000000000000ffffffffffffffffffffffffffffffff", "02000000000000000000000000000000", "03000000000000000000000000000000"},
	{"0100000000000000000000000000000000000000000000000000000000000000", "fffffffffffffffffffffffffffffffff0ffffffffffffffffffffffffffffff11000000000000000000000000000000", "05000000000000000000000000000000"},
	{"0100000000000000000000000000000000000000000000000000000000000000", "fffffffffffffffffffffffffffffffffbfefefefefefefefefefefefefefefe01010101010101010101010101010101", "00000000000000000000000000000000"},
	{"0200000000000000000000000000000000000000000000000000000000000000", "fdffffffffffffffffffffffffffffff", "faffffffffffffffffffffffffffffff"},
	{"0100000000000000040000000000000000000000000000000000000000000000", "e33594d7505e43b900000000000000003394d7505e4379cd01000000000000000000000000000000000000000000000001000000000000000000000000000000", "14000000000000005500000000000000"},
	{"0100000000000000040000000000000000000000000000000000000000000000", "e33594d7505e43b900000000000000003394d7505e4379cd010000000000000000000000000000000000000000000000", "13000000000000000000000000000000"},
}

// rfcIetfText is A.3 #2's and #3's 375-byte message (the IETF
// contribution text), as hex.
const rfcIetfText = "416e79207375626d697373696f6e20746f20746865204945544620696e74656e6465642062792074686520436f6e7472696275746f7220666f72207075626c69636174696f6e20617320616c6c206f722070617274206f6620616e204945544620496e7465726e65742d4472616674206f722052464320616e6420616e792073746174656d656e74206d6164652077697468696e2074686520636f6e74657874206f6620616e204945544620616374697669747920697320636f6e7369646572656420616e20224945544620436f6e747269627574696f6e222e20537563682073746174656d656e747320696e636c756465206f72616c2073746174656d656e747320696e20494554462073657373696f6e732c2061732077656c6c206173207772697474656e20616e6420656c656374726f6e696320636f6d6d756e69636174696f6e73206d61646520617420616e792074696d65206f7220706c6163652c207768696368206172652061646472657373656420746f"

// rfc8439A4 is RFC 8439 Appendix A.4: the Poly1305 one-time key, ChaCha20
// block 0's first 32 bytes. From the RFC's text.
var rfc8439A4 = []struct{ key, nonce, otk string }{
	{strings.Repeat("00", 32), strings.Repeat("00", 12), "76b8e0ada0f13d90405d6ae55386bd28bdd219b8a08ded1aa836efcc8b770dc7"},
	{strings.Repeat("00", 31) + "01", strings.Repeat("00", 11) + "02", "ecfa254f845f647473d3cb140da9e87606cb33066c447b87bc2666dde3fbb739"},
	{"1c9240a5eb55d38af333888604f6b5f0473917c1402b80099dca5cbc207075c0", strings.Repeat("00", 11) + "02", "965e3bc6f9ec7ed9560808f4d229f94b137ff275ca9b3fcbdd59deaad23310ae"},
}

// rfc8439A5 is RFC 8439 Appendix A.5, the AEAD decryption vector. From
// google/s2a-go v0.1.9's chachapoly_test.go, checked against the RFC's
// text.
var rfc8439A5 = struct{ key, nonce, aad, ct, tag, plain string }{
	key:   "1c9240a5eb55d38af333888604f6b5f0473917c1402b80099dca5cbc207075c0",
	nonce: "000000000102030405060708",
	aad:   "f33388860000000000004e91",
	ct:    "64a0861575861af460f062c79be643bd5e805cfd345cf389f108670ac76c8cb24c6cfc18755d43eea09ee94e382d26b0bdb7b73c321b0100d4f03b7f355894cf332f830e710b97ce98c8a84abd0b948114ad176e008d33bd60f982b1ff37c8559797a06ef4f0ef61c186324e2b3506383606907b6a7c02b0f9f6157b53c867e4b9166c767b804d46a59b5216cde7a4e99040c5a40433225ee282a1b0a06c523eaf4534d7f83fa1155b0047718cbc546a0d072b04b3564eea1b422273f548271a0bb2316053fa76991955ebd63159434ecebb4e466dae5a1073a6727627097a1049e617d91d361094fa68f0ff77987130305beaba2eda04df997b714d6c6f2c29a6ad5cb4022b02709b",
	tag:   "eead9d67890cbb22392336fea1851f38",
	plain: "496e7465726e65742d4472616674732061726520647261667420646f63756d656e74732076616c696420666f722061206d6178696d756d206f6620736978206d6f6e74687320616e64206d617920626520757064617465642c207265706c616365642c206f72206f62736f6c65746564206279206f7468657220646f63756d656e747320617420616e792074696d652e20497420697320696e617070726f70726961746520746f2075736520496e7465726e65742d447261667473206173207265666572656e6365206d6174657269616c206f7220746f2063697465207468656d206f74686572207468616e206173202fe2809c776f726b20696e2070726f67726573732e2fe2809d",
}

// cryptoReviewHelpers are the Mk helpers of the programs below.
const cryptoReviewHelpers = `
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
`

// cryptoReviewProgram is a Mk program of one function per body, called in
// order by Main.
func cryptoReviewProgram(bodies []string) string {
	var b strings.Builder
	b.WriteString(cryptoPrelude)
	b.WriteString(cryptoReviewHelpers)
	for i, body := range bodies {
		fmt.Fprintf(&b, "\nV%d(out: mut Writer, a: &Arena): WriteError|OutOfMemory|crypto.Error!void {\n%s}\n", i, body)
	}
	b.WriteString("\nexport Main(sys: &System): WriteError|OutOfMemory|crypto.Error!void {\n    var out := sys.Stdout()\n")
	for i := range bodies {
		fmt.Fprintf(&b, "    try V%d(mut out, sys.arena)\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

// TestSTDCRYPTO_RFC8439AppendixA: RFC 8439 A.3's eleven Poly1305
// vectors (through PolyMac), A.4's three one-time keys (ChaCha20 block 0)
// and A.5's decryption vector (Open, then Open with a flipped tag bit,
// which is BadTag with out unchanged, then Seal of the plaintext): first
// the Go models give the published values, then std.crypto does, in a dev
// and a release build.
func TestSTDCRYPTO_RFC8439AppendixA(t *testing.T) {
	for i, v := range rfc8439A3 {
		tag := polyModel(cryptoUnhex(t, v.key), cryptoUnhex(t, v.msg))
		if got := hex.EncodeToString(tag[:]); got != v.tag {
			t.Errorf("A.3 #%d: model %s, want %s", i+1, got, v.tag)
		}
	}
	for i, v := range rfc8439A4 {
		blk := ccModelBlock(cryptoUnhex(t, v.key), cryptoUnhex(t, v.nonce), 0)
		if got := hex.EncodeToString(blk[:32]); got != v.otk {
			t.Errorf("A.4 #%d: model %s, want %s", i+1, got, v.otk)
		}
	}
	a5 := rfc8439A5
	sealed := aeadModelSeal(cryptoUnhex(t, a5.key), cryptoUnhex(t, a5.nonce), cryptoUnhex(t, a5.aad), cryptoUnhex(t, a5.plain))
	if got := hex.EncodeToString(sealed); got != a5.ct+a5.tag {
		t.Errorf("A.5: model %s", got)
	}
	if t.Failed() {
		t.FailNow()
	}
	var bodies, want []string
	for _, v := range rfc8439A3 {
		bodies = append(bodies, fmt.Sprintf(`    k := try Bytes(a, %q)
    m := try Bytes(a, %q)
    tag := crypto.FxPolyMac(k, m)
    try Print(out, "P ")
    try Hex(out, tag[..])
    try Print(out, "\n")
`, v.key, v.msg))
		want = append(want, "P "+v.tag)
	}
	for _, v := range rfc8439A4 {
		bodies = append(bodies, fmt.Sprintf(`    k := Arr32(try Bytes(a, %q))
    n := Arr12(try Bytes(a, %q))
    blk := crypto.FxChaBlock(&k, &n, 0)
    try Print(out, "K ")
    try Hex(out, blk[0..32])
    try Print(out, "\n")
`, v.key, v.nonce))
		want = append(want, "K "+v.otk)
	}
	bodies = append(bodies, fmt.Sprintf(`    k := Arr32(try Bytes(a, %q))
    n := Arr12(try Bytes(a, %q))
    ad := try Bytes(a, %q)
    s := try Bytes(a, %q)
    p := try Bytes(a, %q)
    back := try a.Slice[u8](s.Len() - 16)
    r := OpenRes(&k, &n, ad, s, mut back[..])
    try Print(out, "O {r} ", (r: r))
    try Hex(out, back[..])
    try Print(out, "\n")
    for i in 0..back.Len() {
        back[i] = 0x5A
    }
    bad := try a.Slice[u8](s.Len())
    for i in 0..s.Len() {
        bad[i] = s[i]
    }
    last := s.Len() - 1
    bad[last] = bad[last] ^ 0x40
    rt := OpenRes(&k, &n, ad, bad[..], mut back[..])
    var untouched := true
    for i in 0..back.Len() {
        if back[i] != 0x5A {
            untouched = false
        }
    }
    try Print(out, "T {rt} {untouched}\n", (rt: rt, untouched: untouched))
    sealed := try a.Slice[u8](p.Len() + 16)
    try crypto.ChaCha20Poly1305.Seal(&k, &n, ad, p, mut sealed[..])
    try Print(out, "S ")
    try Hex(out, sealed[..])
    try Print(out, "\n")
`, a5.key, a5.nonce, a5.aad, a5.ct+a5.tag, a5.plain))
	want = append(want, "O 0 "+a5.plain, "T 1 true", "S "+a5.ct+a5.tag)
	src := cryptoReviewProgram(bodies)
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("RFC 8439 appendix A %v", m), cryptoRun(t, src, m...), strings.Join(want, "\n")+"\n")
	}
}

// cryptoLimitMain prints AeadLenOk at the limit's edges (0, AeadMax - 1,
// AeadMax = 2^38 - 64, AeadMax + 1, 2^38, the largest int), then ChaXor
// from counter 2^32 - 1, the last block Seal may use, over 64 bytes, from
// 2^32 - 2 over 128 bytes, and from 2^32 - 1 over 130 bytes, where the
// counter wraps to 0 (+%, as the model's uint32): the keystream of block 0,
// the Poly1305 key, which is why AeadLenOk stops one block before.
const cryptoLimitMain = cryptoPrelude + `
export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    l0 := crypto.FxAeadLenOk(0)
    l1 := crypto.FxAeadLenOk(274_877_906_879)
    l2 := crypto.FxAeadLenOk(274_877_906_880)
    l3 := crypto.FxAeadLenOk(274_877_906_881)
    l4 := crypto.FxAeadLenOk(274_877_906_944)
    l5 := crypto.FxAeadLenOk(9_223_372_036_854_775_807)
    try Print(mut out, "L {l0} {l1} {l2} {l3} {l4} {l5}\n", (l0: l0, l1: l1, l2: l2, l3: l3, l4: l4, l5: l5))
    var r := Rng(s: 38)
    var kb: [32]u8 = [0; 32]
    var nb: [12]u8 = [0; 12]
    Fill(mut r, mut kb[..])
    Fill(mut r, mut nb[..])
    src := try sys.arena.Slice[u8](130)
    Fill(mut r, mut src[..])
    d64 := try sys.arena.Slice[u8](64)
    crypto.FxChaXor(&kb, &nb, 0xFFFFFFFF, src[0..64], mut d64[..])
    try Print(mut out, "X ")
    try Hex(mut out, d64[..])
    try Print(mut out, "\n")
    d128 := try sys.arena.Slice[u8](128)
    crypto.FxChaXor(&kb, &nb, 0xFFFFFFFE, src[0..128], mut d128[..])
    try Print(mut out, "Y ")
    try Hex(mut out, d128[..])
    try Print(mut out, "\n")
    d130 := try sys.arena.Slice[u8](130)
    crypto.FxChaXor(&kb, &nb, 0xFFFFFFFF, src[..], mut d130[..])
    try Print(mut out, "W ")
    try Hex(mut out, d130[..])
    try Print(mut out, "\n")
}
`

// TestSTDCRYPTO_AeadLengthLimit: Seal's and Open's limit is exactly
// RFC 8439's (2^32 - 1 blocks of 64 bytes from counter 1): AeadLenOk is
// true at 274,877,906,880 and false at 274,877,906,881 and 2^38, so an
// off-by-one or a wrong constant fails here; and ChaXor's keystream at the
// last counters matches RFC 8439 2.4's model, including the wrap that the
// limit prevents (block 2^32 - 1 + 1 is block 0). Dev and release.
func TestSTDCRYPTO_AeadLengthLimit(t *testing.T) {
	r := &cryptoSplitmix{s: 38}
	kb := make([]byte, 32)
	nb := make([]byte, 12)
	r.fill(kb)
	r.fill(nb)
	src := make([]byte, 130)
	r.fill(src)
	if (1<<32-1)*64 != 274_877_906_880 || 1<<38-64 != 274_877_906_880 {
		t.Fatal("the limit's arithmetic is wrong")
	}
	w130 := ccModelXor(kb, nb, 0xFFFFFFFF, src)
	b0 := ccModelBlock(kb, nb, 0)
	for j := 64; j < 128; j++ {
		if w130[j] != src[j]^b0[j-64] {
			t.Fatal("the model does not wrap to block 0 after block 2^32 - 1")
		}
	}
	want := fmt.Sprintf("L true true true false false false\nX %x\nY %x\nW %x\n",
		ccModelXor(kb, nb, 0xFFFFFFFF, src[:64]), ccModelXor(kb, nb, 0xFFFFFFFE, src[:128]), w130)
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("aead limit %v", m), cryptoRun(t, cryptoLimitMain, m...), want)
	}
}

// edLE is x mod L as 32 little-endian bytes.
func edLE(x *big.Int) []byte {
	return reverse(new(big.Int).Mod(x, edL).FillBytes(make([]byte, 32)))
}

// edHram is SHA-512(r || a || m) mod L, RFC 8032's k.
func edHram(r, a, m []byte) *big.Int {
	h := sha512.Sum512(append(append(append([]byte(nil), r...), a...), m...))
	return new(big.Int).Mod(new(big.Int).SetBytes(reverse(h[:])), edL)
}

func edNeg(p edPt) edPt { return edPt{feMod(new(big.Int).Neg(p.x)), p.y} }

func edEq(p, q edPt) bool { return p.x.Cmp(q.x) == 0 && p.y.Cmp(q.y) == 0 }

// edSecretScalar is the clamped scalar s of seed (RFC 8032 5.1.5).
func edSecretScalar(seed []byte) *big.Int {
	h := sha512.Sum512(seed)
	s := append([]byte(nil), h[:32]...)
	s[0] &= 248
	s[31] = s[31]&127 | 64
	return new(big.Int).SetBytes(reverse(s))
}

// edTorsion is the subgroup of order 8, [j]T for j = 0 to 7 with T one of
// edOddKeys' points of order 8.
func edTorsion(t *testing.T) []edPt {
	t.Helper()
	for _, k := range edOddKeys[:7] {
		p, ok := edDecode(cryptoUnhex(t, k))
		if !ok || edEq(edMul(big.NewInt(4), p), edIdentity()) {
			continue
		}
		var out []edPt
		for j := int64(0); j < 8; j++ {
			out = append(out, edMul(big.NewInt(j), p))
		}
		return out
	}
	t.Fatal("no point of order 8 among edOddKeys")
	return nil
}

// edCase is one Verify input with the verdict its family predicts for Go's
// rules (cofactorless, small-order A accepted, R compared by encoding).
type edCase struct {
	what          string
	pub, sig, msg []byte
	goWants       bool
}

// edAcceptanceCases builds the signature families (the constructions of
// "Taming the many EdDSAs", Chalkias et al. 2020; their Table 6 vectors
// are not available offline, so the same kinds of signature are built
// here from the math/big model):
//   - mixed-order R: R = [r]B + T with T of small order, S = r + k s. The
//     cofactorless equation fails, the cofactored one ([8S]B = [8]R +
//     [8k]A) holds: Go false, a cofactored verifier true;
//   - small-order A with S = 0: R = -[k]A found by trying messages. Go
//     true; libsodium and Zig refuse a small-order or identity A;
//   - mixed-order A = [s]B + T8, S = r + k s: true exactly when 8 divides
//     k, under the cofactorless equation; a cofactored verifier accepts all;
//   - non-canonical R (the identity as y = p + 1, and with the sign bit):
//     the computed R' is the identity, whose canonical encoding differs,
//     so Go is false where a verifier comparing decoded points is true;
//     the canonical identity is the control, true;
//   - non-canonical small-order A (y = p + 1, y = p, the identity with the
//     sign bit) with S = 0: Go decodes A leniently and is true where an
//     RFC 8032 strict decoder refuses.
func edAcceptanceCases(t *testing.T) []edCase {
	t.Helper()
	base := edBase()
	tor := edTorsion(t)
	var out []edCase
	msgFor := func(tag string, i int) []byte { return []byte(fmt.Sprintf("CRY-4 %s %d", tag, i)) }
	sig := func(r []byte, s *big.Int) []byte { return append(append([]byte(nil), r...), edLE(s)...) }
	// Mixed-order R, under two honest keys.
	for ki := 0; ki < 2; ki++ {
		seed := sha512.Sum512([]byte(fmt.Sprintf("CRY-4 key %d", ki)))
		priv := ed25519.NewKeyFromSeed(seed[:32])
		pub := []byte(priv[32:])
		s := edSecretScalar(seed[:32])
		apt, _ := edDecode(pub)
		for j := 0; j < 8; j++ {
			msg := msgFor("mixed R", 8*ki+j)
			rh := sha512.Sum512(append([]byte("CRY-4 r"), msg...))
			r := new(big.Int).Mod(new(big.Int).SetBytes(rh[:]), edL)
			rp := edAdd(edMul(r, base), tor[j])
			renc := edEncode(rp)
			k := edHram(renc, pub, msg)
			sv := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k, s)), edL)
			eight := big.NewInt(8)
			lhs := edMul(new(big.Int).Mul(eight, sv), base)
			rhs := edAdd(edMul(eight, rp), edMul(new(big.Int).Mul(eight, k), apt))
			if !edEq(lhs, rhs) {
				t.Fatalf("mixed R %d %d: the cofactored equation does not hold, so the case cannot tell the rules apart", ki, j)
			}
			out = append(out, edCase{fmt.Sprintf("mixed-order R, key %d, torsion %d", ki, j), pub, sig(renc, sv), msg, j == 0})
		}
	}
	// Small-order A, S = 0, R = A or R = the identity.
	for j := 0; j < 8; j++ {
		a := tor[j]
		aenc := edEncode(a)
		for _, rp := range []edPt{a, edIdentity()} {
			renc := edEncode(rp)
			found := false
			for i := 0; i < 400 && !found; i++ {
				msg := msgFor(fmt.Sprintf("small A %d %x", j, renc[:2]), i)
				k := edHram(renc, aenc, msg)
				if edEq(edNeg(edMul(new(big.Int).Mod(k, big.NewInt(8)), a)), rp) {
					out = append(out, edCase{fmt.Sprintf("small-order A %d, S = 0, R %x...", j, renc[:4]), aenc, sig(renc, big.NewInt(0)), msg, true})
					found = true
				}
			}
			if !found {
				t.Fatalf("small A %d: no message found", j)
			}
		}
	}
	// Mixed-order A.
	{
		seed := sha512.Sum512([]byte("CRY-4 mixed A"))
		s := edSecretScalar(seed[:32])
		ap := edAdd(edMul(s, base), tor[1])
		aenc := edEncode(ap)
		var yes, no int
		for i := 0; i < 40; i++ {
			msg := msgFor("mixed A", i)
			rh := sha512.Sum512(append([]byte("CRY-4 r"), msg...))
			r := new(big.Int).Mod(new(big.Int).SetBytes(rh[:]), edL)
			renc := edEncode(edMul(r, base))
			k := edHram(renc, aenc, msg)
			sv := new(big.Int).Mod(new(big.Int).Add(r, new(big.Int).Mul(k, s)), edL)
			ok := new(big.Int).Mod(k, big.NewInt(8)).Sign() == 0
			if ok {
				yes++
			} else {
				no++
			}
			out = append(out, edCase{fmt.Sprintf("mixed-order A, message %d", i), aenc, sig(renc, sv), msg, ok})
		}
		if yes == 0 || no == 0 {
			t.Fatalf("mixed A: %d accepted, %d refused; the family must have both", yes, no)
		}
	}
	// Non-canonical R, and the canonical control, under A of order 8.
	{
		a := tor[1]
		aenc := edEncode(a)
		for _, rc := range []struct {
			enc  string
			want bool
		}{
			{"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", false},
			{"0100000000000000000000000000000000000000000000000000000000000080", false},
			{"0100000000000000000000000000000000000000000000000000000000000000", true},
		} {
			renc := cryptoUnhex(t, rc.enc)
			found := false
			for i := 0; i < 400 && !found; i++ {
				msg := msgFor("R "+rc.enc[:4]+rc.enc[62:], i)
				if new(big.Int).Mod(edHram(renc, aenc, msg), big.NewInt(8)).Sign() == 0 {
					out = append(out, edCase{"R " + rc.enc, aenc, sig(renc, big.NewInt(0)), msg, rc.want})
					found = true
				}
			}
			if !found {
				t.Fatalf("R %s: no message found", rc.enc)
			}
		}
	}
	// Non-canonical encodings of small-order A, S = 0, R the identity.
	for _, ae := range []string{
		"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"0100000000000000000000000000000000000000000000000000000000000080",
	} {
		aenc := cryptoUnhex(t, ae)
		a, ok := edDecode(aenc)
		if !ok {
			t.Fatalf("A %s does not decode in the model", ae)
		}
		renc := edEncode(edIdentity())
		found := false
		for i := 0; i < 400 && !found; i++ {
			msg := msgFor("A "+ae[:4], i)
			if edEq(edMul(new(big.Int).Mod(edHram(renc, aenc, msg), big.NewInt(8)), a), edIdentity()) {
				out = append(out, edCase{"non-canonical A " + ae, aenc, sig(renc, big.NewInt(0)), msg, true})
				found = true
			}
		}
		if !found {
			t.Fatalf("A %s: no message found", ae)
		}
	}
	return out
}

// TestSTDCRYPTO_VerifyAcceptanceSet: Verify accepts exactly what Go's
// crypto/ed25519.Verify accepts on the signatures of edAcceptanceCases, so
// a cofactored Verify, one that refuses a small-order or mixed-order A, or
// one that compares decoded R points fails here. Each family's predicted
// verdict is first checked against Go itself (a wrong construction fails
// before the Mk side is judged); the Mk side's want is Go's verdict,
// computed now. Dev and release.
func TestSTDCRYPTO_VerifyAcceptanceSet(t *testing.T) {
	cases := edAcceptanceCases(t)
	var cmds, want []string
	for _, c := range cases {
		got := ed25519.Verify(c.pub, c.msg, c.sig)
		if got != c.goWants {
			t.Fatalf("%s: Go's Verify says %v, the family predicts %v", c.what, got, c.goWants)
		}
		cmds = append(cmds, "V "+edHex(c.pub)+" "+edHex(c.sig)+" "+edHex(c.msg))
		want = append(want, fmt.Sprintf("V %v", got))
	}
	t.Logf("%d acceptance cases", len(cases))
	cryptoEdRun(t, "verify acceptance set", cmds, want)
}
