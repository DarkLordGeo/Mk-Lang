// Why: X25519 and its field arithmetic must match RFC 7748 and Go everywhere,
// small-order points and non-canonical inputs included.
// What: TestSTDCRYPTO_X25519ModelMatchesRFC7748 and
// TestSTDCRYPTO_FieldAndX25519MatchModels.
// How: GF(2^255 - 19) against math/big, also over chains of operations; the
// ladder at 1 and 1,000 steps and crypto/ecdh on seeded inputs, dev and release.

package std

import (
	"crypto/ecdh"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

var feP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))

// feLE reads 32 bytes little-endian with bit 255 cleared (FeFromBytes).
func feLE(b []byte) *big.Int {
	r := make([]byte, 32)
	for i := 0; i < 32; i++ {
		r[31-i] = b[i]
	}
	r[0] &= 0x7f
	return new(big.Int).SetBytes(r)
}

// feBytes is the canonical encoding of x mod p.
func feBytes(x *big.Int) []byte {
	v := new(big.Int).Mod(x, feP)
	be := v.FillBytes(make([]byte, 32))
	out := make([]byte, 32)
	for i := 0; i < 32; i++ {
		out[i] = be[31-i]
	}
	return out
}

func feExp(x *big.Int, e *big.Int) *big.Int {
	return new(big.Int).Exp(new(big.Int).Mod(x, feP), e, feP)
}

// feEdges are field inputs at the representation's edges: 0, 1, 19, p - 1,
// p, p + 1, 2^255 - 1 (all ones, and bit 255 set too), limb boundaries.
func feEdges(t *testing.T) [][]byte {
	hexes := []string{
		strings.Repeat("00", 32),
		"01" + strings.Repeat("00", 31),
		"13" + strings.Repeat("00", 31),
		"ec" + strings.Repeat("ff", 30) + "7f",
		"ed" + strings.Repeat("ff", 30) + "7f",
		"ee" + strings.Repeat("ff", 30) + "7f",
		strings.Repeat("ff", 31) + "7f",
		strings.Repeat("ff", 32),
		"ffffffffffff07" + strings.Repeat("00", 25), // 2^51 - 1
		"00000000000008" + strings.Repeat("00", 25), // 2^51
		strings.Repeat("00", 31) + "40",             // 2^254
	}
	var out [][]byte
	for _, h := range hexes {
		out = append(out, cryptoUnhex(t, h))
	}
	return out
}

func feWant(a, b []byte) (ops string, flags uint64) {
	x, y := feLE(a), feLE(b)
	var o strings.Builder
	two := big.NewInt(2)
	add := func(v *big.Int) { o.WriteString(hex.EncodeToString(feBytes(v))) }
	add(new(big.Int).Add(x, y))
	add(new(big.Int).Sub(x, y))
	add(new(big.Int).Mul(x, y))
	add(new(big.Int).Mul(x, x))
	add(new(big.Int).Neg(x))
	add(feExp(x, new(big.Int).Sub(feP, two)))
	add(new(big.Int).Mul(x, big.NewInt(121665)))
	add(feExp(x, new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 252), big.NewInt(3))))
	xr := new(big.Int).Mod(x, feP)
	flags = uint64(xr.Bit(0))
	if xr.Sign() == 0 {
		flags |= 2
	}
	if xr.Cmp(new(big.Int).Mod(y, feP)) == 0 {
		flags |= 4
	}
	return o.String(), flags
}

func feChainWant(a, b []byte, n int) string {
	x, y := feLE(a), feLE(b)
	m := func(v *big.Int) *big.Int { return v.Mod(v, feP) }
	for i := 0; i < n; i++ {
		x = m(new(big.Int).Mul(x, y))
		y = m(new(big.Int).Add(x, y))
		x = m(new(big.Int).Sub(x, new(big.Int).Mul(y, y)))
		y = m(new(big.Int).Mul(new(big.Int).Neg(y), big.NewInt(121665)))
		x = m(new(big.Int).Add(new(big.Int).Add(x, x), y))
	}
	return hex.EncodeToString(feBytes(new(big.Int).Mul(x, y)))
}

// x25519Vectors are RFC 7748 section 5.2's two vectors.
var x25519Vectors = []struct{ k, u, want string }{
	{"a546e36bf0527c9d3b16154b82465edd62144c0ac1fc5a18506a2244ba449ac4", "e6db6867583030db3594c1a424b15f7c726624ec26b3353b10a903a6d0ab1c4c", "c3da55379de9c6908e94ea4df28d084f32eccf03491c71f754b4075577a28552"},
	{"4b66e9d4d1b4673c5ad22691957d6af5c11b6421e0ea01d42ca4169e7918ba0d", "e5210f12786811d3f4b7959d0538ae2c31dbe7106fc03c3efc4cd549c715a493", "95cbde9476e8907d7aade45cb4b873f88b595a68799fa152e6f8f7647aac7957"},
}

// RFC 7748 5.2: k = u = 9, then k, u = X25519(k, u), k.
const (
	x25519Iter1    = "422c8e7a6227d7bca1350b3e2bb7279f7897b87bb6854b783c60e80311ae3079"
	x25519Iter1000 = "684cf59ba83309552800ef566f2f4d3c1c3887c49360e3875f2eb94d99532c51"
)

// x25519LowOrder are u values whose result is zero for every scalar: 0,
// 1, the two points of order 8, p - 1, p, p + 1 (as libsodium and Go's
// tests list them), and 2^255 - 1 with bit 255 set (reduces to 18).
var x25519LowOrder = []string{
	strings.Repeat("00", 32),
	"01" + strings.Repeat("00", 31),
	"e0eb7a7c3b41b8ae1656e3faf19fc46ada098deb9c32b1fd866205165f49b800",
	"5f9c95bca3508c24b1d0b1559c83ef5b04445cc4581c8e86d8224eddd09f1157",
	"ec" + strings.Repeat("ff", 30) + "7f",
	"ed" + strings.Repeat("ff", 30) + "7f",
	"ee" + strings.Repeat("ff", 30) + "7f",
}

func goX25519(t *testing.T, k, u []byte) (string, bool) {
	t.Helper()
	priv, err := ecdh.X25519().NewPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ecdh.X25519().NewPublicKey(u)
	if err != nil {
		t.Fatal(err)
	}
	s, err := priv.ECDH(pub)
	if err != nil {
		return "", false // crypto/ecdh refuses an all-zero result
	}
	return hex.EncodeToString(s), true
}

// TestSTDCRYPTO_X25519ModelMatchesRFC7748: crypto/ecdh gives RFC 7748's
// values, so it can judge the Mk code.
func TestSTDCRYPTO_X25519ModelMatchesRFC7748(t *testing.T) {
	for i, v := range x25519Vectors {
		if got, _ := goX25519(t, cryptoUnhex(t, v.k), cryptoUnhex(t, v.u)); got != v.want {
			t.Errorf("RFC 7748 vector %d: crypto/ecdh %s, want %s", i+1, got, v.want)
		}
	}
	k := cryptoUnhex(t, "09"+strings.Repeat("00", 31))
	u := append([]byte(nil), k...)
	for i := 1; i <= 1000; i++ {
		r, _ := goX25519(t, k, u)
		u, k = k, cryptoUnhex(t, r)
		if i == 1 && r != x25519Iter1 || i == 1000 && r != x25519Iter1000 {
			t.Errorf("RFC 7748 iteration %d: crypto/ecdh %s", i, r)
		}
	}
}

const cryptoX25519Main = cryptoPrelude + `
Line(out: mut Writer, tag: str, k: &[32]u8, u: &[32]u8): WriteError!void {
    r := crypto.X25519(k, u)
    try Print(out, "{tag} ", (tag: tag))
    try Hex(out, r[..])
    try Print(out, "\n")
}

FeLine(out: mut Writer, a: &[32]u8, b: &[32]u8): WriteError!void {
    ops := crypto.FxFeOps(a, b)
    f := crypto.FxFeFlags(a, b)
    ch := crypto.FxFeChain(a, b, 50)
    try Print(out, "F ")
    try Hex(out, ops[..])
    try Print(out, " {f} ", (f: f))
    try Hex(out, ch[..])
    try Print(out, "\n")
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    var buf: [32]u8 = [0; 32]
    VECTORS
    var k: [32]u8 = [0; 32]
    k[0] = 9
    var u := k
    for i in 1..1001 {
        r := crypto.X25519(&k, &u)
        u = k
        k = r
        if i == 1 || i == 1000 {
            try Print(mut out, "I {i} ", (i: i))
            try Hex(mut out, r[..])
            try Print(mut out, "\n")
        }
    }
    var rng := Rng(s: 25519)
    var rk: [32]u8 = [0; 32]
    var ru: [32]u8 = [0; 32]
    for _ in 0..200 {
        Fill(mut rng, mut rk[..])
        Fill(mut rng, mut ru[..])
        try Line(mut out, "R", &rk, &ru)
    }
    for _ in 0..300 {
        Fill(mut rng, mut rk[..])
        Fill(mut rng, mut ru[..])
        try FeLine(mut out, &rk, &ru)
    }
}
`

// TestSTDCRYPTO_FieldAndX25519MatchModels: the field operations and
// X25519 in Mk against math/big and crypto/ecdh (see the file comment).
func TestSTDCRYPTO_FieldAndX25519MatchModels(t *testing.T) {
	var vec, want strings.Builder
	for i, v := range x25519Vectors {
		fmt.Fprintf(&vec, "Unhex(%q, mut buf[..])\n    vk%d := buf\n    Unhex(%q, mut buf[..])\n    try Line(mut out, \"V\", &vk%d, &buf)\n    ", v.k, i, v.u, i)
		fmt.Fprintf(&want, "V %s\n", v.want)
	}
	scalars := []string{x25519Vectors[0].k, strings.Repeat("ff", 32), strings.Repeat("00", 32)}
	for i, s := range scalars {
		for j, u := range x25519LowOrder {
			fmt.Fprintf(&vec, "Unhex(%q, mut buf[..])\n    lk%d_%d := buf\n    Unhex(%q, mut buf[..])\n    try Line(mut out, \"Z\", &lk%d_%d, &buf)\n    ", s, i, j, u, i, j)
			fmt.Fprintf(&want, "Z %s\n", strings.Repeat("00", 32))
		}
		// u + p for u = 9: the same result as u = 9 (non-canonical u), and
		// u = 9 with bit 255 set.
		for j, u := range []string{"f6" + strings.Repeat("ff", 30) + "7f", "09" + strings.Repeat("00", 30) + "80"} {
			fmt.Fprintf(&vec, "Unhex(%q, mut buf[..])\n    nk%d_%d := buf\n    Unhex(%q, mut buf[..])\n    try Line(mut out, \"N\", &nk%d_%d, &buf)\n    ", s, i, j, u, i, j)
			r, _ := goX25519(t, cryptoUnhex(t, s), cryptoUnhex(t, "09"+strings.Repeat("00", 31)))
			fmt.Fprintf(&want, "N %s\n", r)
		}
	}
	fmt.Fprintf(&want, "I 1 %s\nI 1000 %s\n", x25519Iter1, x25519Iter1000)
	r := &cryptoSplitmix{s: 25519}
	for i := 0; i < 200; i++ {
		k, u := make([]byte, 32), make([]byte, 32)
		r.fill(k)
		r.fill(u)
		got, ok := goX25519(t, k, u)
		if !ok {
			t.Fatalf("seeded case %d is a small-order point; change the seed", i)
		}
		fmt.Fprintf(&want, "R %s\n", got)
	}
	edges := feEdges(t)
	for i := 0; i < 300; i++ {
		a, b := make([]byte, 32), make([]byte, 32)
		r.fill(a)
		r.fill(b)
		// The first cases pair the edge values with each other.
		if i < len(edges)*len(edges) {
			copy(a, edges[i/len(edges)])
			copy(b, edges[i%len(edges)])
		}
		ops, flags := feWant(a, b)
		fmt.Fprintf(&want, "F %s %d %s\n", ops, flags, feChainWant(a, b, 50))
	}
	src := strings.Replace(cryptoX25519Main, "VECTORS", vec.String(), 1)
	// The Mk side pairs the edges the same way.
	src = strings.Replace(src, `    for _ in 0..300 {
        Fill(mut rng, mut rk[..])
        Fill(mut rng, mut ru[..])
        try FeLine(mut out, &rk, &ru)
    }`, feEdgeLoop(edges), 1)
	for _, m := range cryptoModes {
		cryptoCompare(t, fmt.Sprintf("x25519 %v", m), cryptoRun(t, src, m...), want.String())
	}
}

// feEdgeLoop is the Mk loop of the field cases: seeded pairs, the first
// len(edges)^2 of them replaced by the edge pairs.
func feEdgeLoop(edges [][]byte) string {
	var b strings.Builder
	b.WriteString("    var edges: [" + fmt.Sprint(len(edges)) + "][32]u8 = [[0; 32]; " + fmt.Sprint(len(edges)) + "]\n")
	for i, e := range edges {
		fmt.Fprintf(&b, "    Unhex(%q, mut edges[%d][..])\n", hex.EncodeToString(e), i)
	}
	fmt.Fprintf(&b, `    for i in 0..300 {
        Fill(mut rng, mut rk[..])
        Fill(mut rng, mut ru[..])
        if i < %d {
            rk = edges[i / %d]
            ru = edges[i %% %d]
        }
        try FeLine(mut out, &rk, &ru)
    }`, len(edges)*len(edges), len(edges), len(edges))
	return b.String()
}
