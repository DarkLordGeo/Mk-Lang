// Why: Ed25519 signatures must agree with Go's exactly, including which bad
// signatures and keys Verify refuses.
// What: RFC 8032 and SUPERCOP vectors, Sign and Verify against crypto/ed25519,
// and the scalar and point functions against math/big models.
// How: one Mk program reads commands on stdin, dev and release; Go's answer is
// the specification for bad signatures and keys.

package std

import (
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cryptoEdMain reads one command per line and prints one result line:
//
//	S seed msg        -> S public sig     (FromSeed, then Sign)
//	V public sig msg  -> V bool           (Verify)
//	R h64             -> R s              (ScReduce64)
//	M a b c           -> M s              (ScMulAdd)
//	C s               -> C bool           (ScCanonical)
//	B s               -> B point          (PtBaseMult, encoded)
//	D a point b       -> D point | none   ([a]A + [b]B)
//	E point           -> E point | none   (decode, encode)
const cryptoEdMain = cryptoPrelude + `
Field(a: &Arena, s: str): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](s.Len() / 2)
    Unhex(s, mut out[..])
    return out[..]
}

Arr64(b: []u8): [64]u8 {
    var k: [64]u8 = [0; 64]
    for i in 0..64 {
        k[i] = b[i]
    }
    return k
}

Opt(out: mut Writer, tag: str, r: ?[32]u8): WriteError!void {
    v := r else {
        try Print(out, "{tag} none\n", (tag: tag))
        return
    }
    try Print(out, "{tag} ", (tag: tag))
    try Hex(out, v[..])
    try Print(out, "\n")
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    a := sys.arena
    for true {
        lo := try inp.Line(a)
        line := lo else {
            break
        }
        c := line.AsBytes()[0]
        if c == 83 {
            seed := Arr32(try Field(a, line[2..66]))
            msg := try Field(a, line[67..line.Len()])
            kp := crypto.Ed25519.FromSeed(&seed)
            sig := crypto.Ed25519.Sign(&kp, msg)
            try Print(mut out, "S ")
            try Hex(mut out, kp[32..64])
            try Print(mut out, " ")
            try Hex(mut out, sig[..])
            try Print(mut out, "\n")
        } else if c == 86 {
            pk := Arr32(try Field(a, line[2..66]))
            sig := Arr64(try Field(a, line[67..195]))
            msg := try Field(a, line[196..line.Len()])
            ok := crypto.Ed25519.Verify(&pk, msg, &sig)
            try Print(mut out, "V {ok}\n", (ok: ok))
        } else if c == 82 {
            h := Arr64(try Field(a, line[2..130]))
            s := crypto.FxScReduce64(&h)
            try Print(mut out, "R ")
            try Hex(mut out, s[..])
            try Print(mut out, "\n")
        } else if c == 77 {
            x := Arr32(try Field(a, line[2..66]))
            y := Arr32(try Field(a, line[67..131]))
            z := Arr32(try Field(a, line[132..196]))
            s := crypto.FxScMulAdd(&x, &y, &z)
            try Print(mut out, "M ")
            try Hex(mut out, s[..])
            try Print(mut out, "\n")
        } else if c == 67 {
            s := Arr32(try Field(a, line[2..66]))
            ok := crypto.FxScCanonical(&s)
            try Print(mut out, "C {ok}\n", (ok: ok))
        } else if c == 66 {
            s := Arr32(try Field(a, line[2..66]))
            p := crypto.FxBaseMult(&s)
            try Print(mut out, "B ")
            try Hex(mut out, p[..])
            try Print(mut out, "\n")
        } else if c == 68 {
            x := Arr32(try Field(a, line[2..66]))
            p := Arr32(try Field(a, line[67..131]))
            y := Arr32(try Field(a, line[132..196]))
            try Opt(mut out, "D", crypto.FxDoubleMult(&x, &p, &y))
        } else if c == 69 {
            p := Arr32(try Field(a, line[2..66]))
            try Opt(mut out, "E", crypto.FxDecode(&p))
        }
    }
}
`

// cryptoEdRun runs cryptoEdMain on the commands in every build mode and
// compares with want.
func cryptoEdRun(t *testing.T, what string, cmds []string, want []string) {
	t.Helper()
	in := strings.Join(cmds, "\n") + "\n"
	for _, m := range cryptoModes {
		exe, out, status := cryptoTry(t, cryptoEdMain, m...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk build %v: exit %d\n%s", m, status, out)
		}
		r := runNative(t, exe, in)
		if r.code != 0 {
			t.Fatalf("%s %v: exit %d\n%s", what, m, r.code, r.stderr)
		}
		cryptoCompare(t, fmt.Sprintf("%s %v", what, m), strings.ReplaceAll(r.stdout, "\r\n", "\n"), strings.Join(want, "\n")+"\n")
	}
}

// RFC 8032 7.1, tests 1 to 3: seed, public key, message, signature.
var rfc8032Vectors = []struct{ seed, pub, msg, sig string }{
	{"9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60", "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a", "",
		"e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b"},
	{"4ccd089b28ff96da9db6c346ec114e0f5b8a319f35aba624da8cf6ed4fb8a6fb", "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c", "72",
		"92a009a9f0d4cab8720e820b5f642540a2b27b5416503f8fb3762223ebdb69da085ac1e43e15996e458f3613d0f11d8c387b2eaeb4302aeeb00d291612bb0c00"},
	{"c5aa8df43f9f837bedb7442f31dcb7b166d38535076f094b85ce3a2e0b4458f7", "fc51cd8e6218a1a38da47ed00230f0580816ed13ba3303ac5deb911548908025", "af82",
		"6291d657deec24024827e69c3abe01a30ce548a284743a445e3680d7db5ac3ac18ff9b538d16f290ae67f760984dc6594a7c15e9716ed28dc027beceea1ec40a"},
}

// supercopVectors reads Go's sign.input.gz: secret (seed || public),
// public, message, signature || message, colon-separated.
func supercopVectors(t *testing.T) [][4]string {
	t.Helper()
	f, err := os.Open(filepath.Join(runtime.GOROOT(), "src", "crypto", "ed25519", "testdata", "sign.input.gz"))
	if err != nil {
		t.Skipf("Go's sign.input.gz not found: %v", err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out [][4]string
	sc := bufio.NewScanner(z)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) != 5 {
			t.Fatalf("bad line %q", sc.Text())
		}
		out = append(out, [4]string{p[0][:64], p[1], p[2], p[3][:128]})
	}
	// Go 1.27.3's file holds 128 of SUPERCOP's lines.
	if len(out) < 128 {
		t.Fatalf("%d vectors, want at least 128", len(out))
	}
	return out
}

// TestSTDCRYPTO_Ed25519ModelMatchesRFC8032: Go's crypto/ed25519 gives RFC
// 8032's signatures, so it can judge the Mk code.
func TestSTDCRYPTO_Ed25519ModelMatchesRFC8032(t *testing.T) {
	for i, v := range rfc8032Vectors {
		k := ed25519.NewKeyFromSeed(cryptoUnhex(t, v.seed))
		if got := hex.EncodeToString(k[32:]); got != v.pub {
			t.Errorf("test %d: public %s, want %s", i+1, got, v.pub)
		}
		if got := hex.EncodeToString(ed25519.Sign(k, cryptoUnhex(t, v.msg))); got != v.sig {
			t.Errorf("test %d: signature %s, want %s", i+1, got, v.sig)
		}
	}
}

// TestSTDCRYPTO_Ed25519PublishedVectorsInMk: RFC 8032 tests 1 to 3 and the
// SUPERCOP vectors Go carries (128): the public key and the signature from the seed,
// and Verify of the published signature.
func TestSTDCRYPTO_Ed25519PublishedVectorsInMk(t *testing.T) {
	var cmds, want []string
	add := func(seed, pub, msg, sig string) {
		cmds = append(cmds, "S "+seed+" "+msg, "V "+pub+" "+sig+" "+msg)
		want = append(want, "S "+pub+" "+sig, "V true")
	}
	for _, v := range rfc8032Vectors {
		add(v.seed, v.pub, v.msg, v.sig)
	}
	for _, v := range supercopVectors(t) {
		add(v[0], v[1], v[2], v[3])
	}
	cryptoEdRun(t, "ed25519 vectors", cmds, want)
}

func edHex(b []byte) string { return hex.EncodeToString(b) }

// edMutations are the bad signatures and keys every Verify case tries:
// each is a (public, sig, msg) triple and Go's answer.
func edMutations(pub, sig, msg []byte) [][3][]byte {
	cp := func(b []byte) []byte { return append([]byte(nil), b...) }
	var out [][3][]byte
	add := func(p, s, m []byte) { out = append(out, [3][]byte{p, s, m}) }
	add(pub, sig, msg)
	for _, bit := range []int{0, 7, 200, 255, 256, 300, 503, 511} {
		s := cp(sig)
		s[bit/8] ^= 1 << (bit % 8)
		add(pub, s, msg)
	}
	p := cp(pub)
	p[3] ^= 0x10
	add(p, sig, msg)
	if len(msg) > 0 {
		m := cp(msg)
		m[len(m)-1] ^= 1
		add(pub, sig, m)
	}
	// S + L: the same point equation, refused because S is not canonical.
	s := new(big.Int).SetBytes(reverse(sig[32:]))
	s.Add(s, edL)
	sl := cp(sig)
	copy(sl[32:], reverse(s.FillBytes(make([]byte, 32))))
	add(pub, sl, msg)
	// S = L exactly, and S with a top bit set.
	sL := cp(sig)
	copy(sL[32:], reverse(edL.FillBytes(make([]byte, 32))))
	add(pub, sL, msg)
	sT := cp(sig)
	sT[63] |= 0x80
	add(pub, sT, msg)
	return out
}

func reverse(b []byte) []byte {
	r := make([]byte, len(b))
	for i := range b {
		r[len(b)-1-i] = b[i]
	}
	return r
}

// edOddKeys are public keys at the decoding's edges: the identity, the
// other small-order points (orders 2, 4 and 8 from "Taming the many
// EdDSAs", Chalkias et al. 2020), y = p (non-canonical 0) and y = p + 1
// (non-canonical 1), y = 2 (not on the curve: no x), all ones, and the
// identity with the sign bit set (x = 0, sign 1, which Go accepts).
var edOddKeys = []string{
	"0100000000000000000000000000000000000000000000000000000000000000",
	"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	"0000000000000000000000000000000000000000000000000000000000000080",
	"0000000000000000000000000000000000000000000000000000000000000000",
	"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a",
	"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac03fa",
	"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05",
	"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
	"0200000000000000000000000000000000000000000000000000000000000000",
	"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	"0100000000000000000000000000000000000000000000000000000000000080",
}

// TestSTDCRYPTO_Ed25519MatchesGo: for seeded seeds and messages of 0 to
// 300 bytes, the public key and signature equal crypto/ed25519's; Verify
// equals crypto/ed25519.Verify on the good signature and on every mutation
// (flipped bits in R, S, the key and the message; S + L; S = L; S's top
// bit), and on the odd public keys with a real signature.
func TestSTDCRYPTO_Ed25519MatchesGo(t *testing.T) {
	var cmds, want []string
	r := &cryptoSplitmix{s: 8032}
	for n := 0; n <= 300; n += 3 {
		seed := make([]byte, 32)
		r.fill(seed)
		msg := make([]byte, n)
		r.fill(msg)
		k := ed25519.NewKeyFromSeed(seed)
		sig := ed25519.Sign(k, msg)
		cmds = append(cmds, "S "+edHex(seed)+" "+edHex(msg))
		want = append(want, "S "+edHex(k[32:])+" "+edHex(sig))
		muts := edMutations(k[32:], sig, msg)
		if n%30 == 0 {
			for _, pk := range edOddKeys {
				muts = append(muts, [3][]byte{cryptoUnhex(t, pk), sig, msg})
			}
		}
		for _, m := range muts {
			cmds = append(cmds, "V "+edHex(m[0])+" "+edHex(m[1])+" "+edHex(m[2]))
			want = append(want, fmt.Sprintf("V %v", ed25519.Verify(m[0], m[2], m[1])))
		}
	}
	cryptoEdRun(t, "ed25519 vs Go", cmds, want)
}

// TestSTDCRYPTO_ScalarAndPointMatchModels: ScReduce64, ScMulAdd and
// ScCanonical against math/big, on edges (0, L - 1, L, L + 1, 2^252,
// 2^253 - 1, 2^256 - 1, 2^512 - 1) and seeded values; PtBaseMult against
// the affine model on clamped, reduced and edge scalars; the double
// multiply and the decoding against the model, the odd keys included.
func TestSTDCRYPTO_ScalarAndPointMatchModels(t *testing.T) {
	var cmds, want []string
	le := func(x *big.Int, n int) []byte { return reverse(x.FillBytes(make([]byte, n))) }
	one := big.NewInt(1)
	pow := func(k uint) *big.Int { return new(big.Int).Lsh(one, k) }
	m1 := func(x *big.Int) *big.Int { return new(big.Int).Sub(x, one) }
	edges := []*big.Int{big.NewInt(0), one, m1(edL), edL, new(big.Int).Add(edL, one), pow(252), m1(pow(253)), m1(pow(255)), m1(pow(256)),
		new(big.Int).Mul(edL, big.NewInt(8)), new(big.Int).Mul(edL, edL)}
	r := &cryptoSplitmix{s: 252}
	var wide []*big.Int
	for _, e := range edges {
		wide = append(wide, e)
	}
	wide = append(wide, m1(pow(512)), new(big.Int).Mul(m1(pow(256)), m1(pow(256))))
	for i := 0; i < 200; i++ {
		b := make([]byte, 64)
		r.fill(b)
		wide = append(wide, new(big.Int).SetBytes(b))
	}
	for _, x := range wide {
		if x.BitLen() > 512 {
			continue
		}
		cmds = append(cmds, "R "+edHex(le(x, 64)))
		want = append(want, "R "+edHex(le(new(big.Int).Mod(x, edL), 32)))
	}
	var narrow []*big.Int
	for _, e := range edges {
		if e.BitLen() <= 256 {
			narrow = append(narrow, e)
		}
	}
	for i := 0; i < 100; i++ {
		b := make([]byte, 32)
		r.fill(b)
		narrow = append(narrow, new(big.Int).SetBytes(b))
	}
	for i, a := range narrow {
		b := narrow[(i*7+3)%len(narrow)]
		c := narrow[(i*13+5)%len(narrow)]
		cmds = append(cmds, "M "+edHex(le(a, 32))+" "+edHex(le(b, 32))+" "+edHex(le(c, 32)), "C "+edHex(le(a, 32)))
		s := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Mul(a, b), c), edL)
		want = append(want, "M "+edHex(le(s, 32)), fmt.Sprintf("C %v", a.Cmp(edL) < 0))
	}
	base := edBase()
	// The first seven odd keys are of small order, as their comment says.
	for i, k := range edOddKeys[:7] {
		p, ok := edDecode(cryptoUnhex(t, k))
		if q := edMul(big.NewInt(8), p); !ok || q.x.Sign() != 0 || q.y.Cmp(one) != 0 {
			t.Errorf("edOddKeys[%d] is not of small order", i)
		}
	}
	var scalars []*big.Int
	for _, e := range []*big.Int{big.NewInt(0), one, big.NewInt(8), m1(edL), edL, m1(pow(255)), pow(254)} {
		scalars = append(scalars, e)
	}
	for i := 0; i < 40; i++ {
		b := make([]byte, 32)
		r.fill(b)
		b[31] &= 0x7f
		if i%2 == 0 {
			b[0] &= 248
			b[31] |= 64
		}
		scalars = append(scalars, new(big.Int).SetBytes(reverse(b)))
	}
	for _, s := range scalars {
		cmds = append(cmds, "B "+edHex(le(s, 32)))
		want = append(want, "B "+edHex(edEncode(edMul(s, base))))
	}
	var points []string
	for i := 0; i < 20; i++ {
		b := make([]byte, 32)
		r.fill(b)
		points = append(points, edHex(edEncode(edMul(new(big.Int).SetBytes(b), base))))
		b2 := make([]byte, 32)
		r.fill(b2)
		points = append(points, edHex(b2)) // random bytes: about half decode
	}
	points = append(points, edOddKeys...)
	for i, p := range points {
		cmds = append(cmds, "E "+p)
		pt, ok := edDecode(cryptoUnhex(t, p))
		if !ok {
			want = append(want, "E none")
		} else {
			want = append(want, "E "+edHex(edEncode(pt)))
		}
		a := scalars[(i*5+7)%len(scalars)]
		b := new(big.Int).Mod(scalars[(i*11+2)%len(scalars)], edL)
		cmds = append(cmds, "D "+edHex(le(a, 32))+" "+p+" "+edHex(le(b, 32)))
		if !ok {
			want = append(want, "D none")
		} else {
			want = append(want, "D "+edHex(edEncode(edAdd(edMul(a, pt), edMul(b, base)))))
		}
	}
	cryptoEdRun(t, "scalar and point", cmds, want)
}

// cryptoRandomMain: Random fills buffers (two draws differ, the empty
// buffer is fine) and is Canceled in a cancelled scope with nothing
// written; Keypair gives a secret whose public half is FromSeed's of its
// seed, whose signatures verify, and two calls give different keys;
// Keypair is Canceled in a cancelled scope.
const cryptoRandomMain = cryptoPrelude + `
export Main(sys: &System): WriteError|Canceled!void {
    var out := sys.Stdout()
    io := sys.io
    var a: [64]u8 = [0; 64]
    var b: [64]u8 = [0; 64]
    try crypto.Random(io, mut a[..])
    try crypto.Random(io, mut b[..])
    var nz := 0
    var same := 0
    for i in 0..64 {
        if a[i] != 0 {
            nz += 1
        }
        if a[i] == b[i] {
            same += 1
        }
    }
    var e: [0]u8 = []
    try crypto.Random(io, mut e[..])
    var big: [100000]u8 = [0; 100000]
    try crypto.Random(io, mut big[..])
    var bz := 0
    for i in 0..100000 {
        if big[i] == 0 {
            bz += 1
        }
    }
    var c: [8]u8 = [0; 8]
    var rc := 0
    var kc := 0
    scope s := io.Scope() {
        s.Cancel()
        crypto.Random(io, mut c[..]) else {
            rc = 1
        }
        _ = crypto.Ed25519.Keypair(io) else {
            kc = 1
        }
    }
    var cz := true
    for i in 0..8 {
        if c[i] != 0 {
            cz = false
        }
    }
    try Print(mut out, "R {nz} {same} {bz} {rc} {cz} {kc}\n", (nz: nz > 32, same: same < 32, bz: bz > 100 && bz < 700, rc: rc, cz: cz, kc: kc))
    k1 := try crypto.Ed25519.Keypair(io)
    k2 := try crypto.Ed25519.Keypair(io)
    var seed: [32]u8 = [0; 32]
    for i in 0..32 {
        seed[i] = k1[i]
    }
    again := crypto.Ed25519.FromSeed(&seed)
    differ := !crypto.Equal(k1[..], k2[..])
    rebuilt := crypto.Equal(k1[..], again[..])
    sig := crypto.Ed25519.Sign(&k1, "message".AsBytes())
    var pk: [32]u8 = [0; 32]
    for i in 0..32 {
        pk[i] = k1[32 + i]
    }
    ok := crypto.Ed25519.Verify(&pk, "message".AsBytes(), &sig)
    bad := crypto.Ed25519.Verify(&pk, "messagE".AsBytes(), &sig)
    try Print(mut out, "K {differ} {rebuilt} {ok} {bad}\n", (differ: differ, rebuilt: rebuilt, ok: ok, bad: bad))
}
`

// TestSTDCRYPTO_RandomAndKeypair runs cryptoRandomMain (dev and release).
// The 100,000-byte draw has between 100 and 700 zero bytes (the expectation
// is 390.6; outside the range is below 2^-100 likely), the 64-byte draws
// fewer than 32 zeros and fewer than 32 equal positions.
func TestSTDCRYPTO_RandomAndKeypair(t *testing.T) {
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("random %v", m), cryptoRun(t, cryptoRandomMain, m...), "R true true true 1 true 1\nK true true true false\n")
	}
}
