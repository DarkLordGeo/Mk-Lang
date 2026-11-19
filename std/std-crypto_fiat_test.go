// Why: the translated fiat-crypto field files must compute what the original
// Montgomery arithmetic computes.
// What: TestSTDCRYPTO_FiatFieldsMatchModel over std/crypto/fiat_*.mk.
// How: a math/big Montgomery model; random elements and edge values (0, 1,
// m - 1, m - 2, R mod m) through every operation, dev and release.

package std

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// cr2FiatFixture exports one entry point over the four fields: field f
// (0 P-256, 1 P-256 n, 2 P-384, 3 P-384 n), operation op, operands as
// little-endian limbs in 48-byte arrays; the result in the same form.
const cr2FiatFixture = `// Fixture of the std-crypto2 tests: the fiat fields, exported.

FxL4(b: &[48]u8): [4]u64 {
    return [Le64(b[..], 0), Le64(b[..], 8), Le64(b[..], 16), Le64(b[..], 24)]
}

FxL6(b: &[48]u8): [6]u64 {
    return [Le64(b[..], 0), Le64(b[..], 8), Le64(b[..], 16), Le64(b[..], 24), Le64(b[..], 32), Le64(b[..], 40)]
}

FxOut(l: []u64): [48]u8 {
    var out: [48]u8 = [0; 48]
    for i in 0..l.Len() {
        PutLe64(mut out[..], 8 * i, l[i])
    }
    return out
}

FxBytes(b: []u8): [48]u8 {
    var out: [48]u8 = [0; 48]
    (mut out[0..b.Len()]).CopyFrom(b)
    return out
}

FxF4(op: int, a: &[4]u64, b: &[4]u64, ab: &[48]u8, f: int): [48]u8 {
    var r: [4]u64 = [0; 4]
    if f == 0 {
        r = match op {
            0 { P256Mul(a, b) }
            1 { P256Square(a) }
            2 { P256Add(a, b) }
            3 { P256Sub(a, b) }
            4 { P256Opp(a) }
            5 { P256FromMontgomery(a) }
            6 { P256ToMontgomery(a) }
            7 { [P256Nonzero(a), 0, 0, 0] }
            8 { P256Selectznz(b[0] & 1, a, b) }
            9 {
                o := P256ToBytes(a)
                return FxBytes(o[..])
            }
            10 {
                var x: [32]u8 = [0; 32]
                (mut x[..]).CopyFrom(ab[0..32])
                P256FromBytes(&x)
            }
            _ { P256SetOne() }
        }
    } else {
        r = match op {
            0 { P256nMul(a, b) }
            1 { P256nSquare(a) }
            2 { P256nAdd(a, b) }
            3 { P256nSub(a, b) }
            4 { P256nOpp(a) }
            5 { P256nFromMontgomery(a) }
            6 { P256nToMontgomery(a) }
            7 { [P256nNonzero(a), 0, 0, 0] }
            8 { P256nSelectznz(b[0] & 1, a, b) }
            9 {
                o := P256nToBytes(a)
                return FxBytes(o[..])
            }
            10 {
                var x: [32]u8 = [0; 32]
                (mut x[..]).CopyFrom(ab[0..32])
                P256nFromBytes(&x)
            }
            _ { P256nSetOne() }
        }
    }
    return FxOut(r[..])
}

FxF6(op: int, a: &[6]u64, b: &[6]u64, ab: &[48]u8, f: int): [48]u8 {
    var r: [6]u64 = [0; 6]
    if f == 2 {
        r = match op {
            0 { P384Mul(a, b) }
            1 { P384Square(a) }
            2 { P384Add(a, b) }
            3 { P384Sub(a, b) }
            4 { P384Opp(a) }
            5 { P384FromMontgomery(a) }
            6 { P384ToMontgomery(a) }
            7 { [P384Nonzero(a), 0, 0, 0, 0, 0] }
            8 { P384Selectznz(b[0] & 1, a, b) }
            9 {
                o := P384ToBytes(a)
                return FxBytes(o[..])
            }
            10 { P384FromBytes(ab) }
            _ { P384SetOne() }
        }
    } else {
        r = match op {
            0 { P384nMul(a, b) }
            1 { P384nSquare(a) }
            2 { P384nAdd(a, b) }
            3 { P384nSub(a, b) }
            4 { P384nOpp(a) }
            5 { P384nFromMontgomery(a) }
            6 { P384nToMontgomery(a) }
            7 { [P384nNonzero(a), 0, 0, 0, 0, 0] }
            8 { P384nSelectznz(b[0] & 1, a, b) }
            9 {
                o := P384nToBytes(a)
                return FxBytes(o[..])
            }
            10 { P384nFromBytes(ab) }
            _ { P384nSetOne() }
        }
    }
    return FxOut(r[..])
}

export FxFiat(f: int, op: int, a: &[48]u8, b: &[48]u8): [48]u8 {
    if f < 2 {
        x := FxL4(a)
        y := FxL4(b)
        return FxF4(op, &x, &y, a, f)
    }
    x := FxL6(a)
    y := FxL6(b)
    return FxF6(op, &x, &y, a, f)
}
`

const cr2FiatMain = `import std.crypto

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var w := sys.Stdout()
    var inp := sys.Stdin()
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    for true {
        lo := try inp.Line(&ar)
        line := lo else {
            break
        }
        var sp := line.Split(" ")
        f := Cast[int]((sp.Next() else { "0" }).ParseInt() else { 0 })
        op := Cast[int]((sp.Next() else { "0" }).ParseInt() else { 0 })
        var a: [48]u8 = [0; 48]
        Unhex(sp.Next() else { "" }, mut a[..])
        var b: [48]u8 = [0; 48]
        Unhex(sp.Next() else { "" }, mut b[..])
        r := crypto.FxFiat(f, op, &a, &b)
        try Hex(mut w, r[..])
        try Print(mut w, "\n")
        ar.Reset()
    }
    try w.Flush()
}
`

// cr2FiatModulus is each field's modulus.
var cr2FiatModulus = func() []*big.Int {
	var out []*big.Int
	for _, s := range []string{
		"ffffffff00000001000000000000000000000000ffffffffffffffffffffffff",
		"ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551",
		"fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffeffffffff0000000000000000ffffffff",
		"ffffffffffffffffffffffffffffffffffffffffffffffffc7634d81f4372ddf581a0db248b0a77aecec196accc52973",
	} {
		m, _ := new(big.Int).SetString(s, 16)
		out = append(out, m)
	}
	return out
}()

// cr2LE is x as 48 little-endian bytes, as hex.
func cr2LE(x *big.Int) string {
	b := make([]byte, 48)
	x.FillBytes(b)
	for i, j := 0, 47; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return hex.EncodeToString(b)
}

// cr2FiatWant is the model's answer.
func cr2FiatWant(f, op int, a, b *big.Int) *big.Int {
	m := cr2FiatModulus[f]
	bits := uint(256)
	if f >= 2 {
		bits = 384
	}
	r := new(big.Int).Lsh(big.NewInt(1), bits)
	rinv := new(big.Int).ModInverse(new(big.Int).Mod(r, m), m)
	mod := func(x *big.Int) *big.Int { return x.Mod(x, m) }
	switch op {
	case 0:
		return mod(new(big.Int).Mul(new(big.Int).Mul(a, b), rinv))
	case 1:
		return mod(new(big.Int).Mul(new(big.Int).Mul(a, a), rinv))
	case 2:
		return mod(new(big.Int).Add(a, b))
	case 3:
		return mod(new(big.Int).Sub(a, b))
	case 4:
		return mod(new(big.Int).Neg(a))
	case 5:
		return mod(new(big.Int).Mul(a, rinv))
	case 6:
		return mod(new(big.Int).Mul(a, r))
	case 7:
		if a.Sign() == 0 {
			return big.NewInt(0)
		}
		return nil // any nonzero word
	case 8:
		if b.Bit(0) == 0 {
			return a
		}
		return b
	case 9, 10:
		return a
	}
	return mod(new(big.Int).Set(r))
}

// TestSTDCRYPTO_FiatFieldsMatchModel: every translated operation of the
// four fields against math/big.
func TestSTDCRYPTO_FiatFieldsMatchModel(t *testing.T) {
	t.Parallel()
	var lines []string
	type c struct {
		f, op int
		a, b  *big.Int
	}
	var cases []c
	rnd := cryptoSplitmix{s: 7}
	for f, m := range cr2FiatModulus {
		r := new(big.Int).Lsh(big.NewInt(1), uint(m.BitLen()))
		edges := []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(m, big.NewInt(1)), new(big.Int).Sub(m, big.NewInt(2)), new(big.Int).Mod(r, m)}
		var vals []*big.Int
		vals = append(vals, edges...)
		for i := 0; i < 40; i++ {
			b := make([]byte, m.BitLen()/8)
			rnd.fill(b)
			vals = append(vals, new(big.Int).Mod(new(big.Int).SetBytes(b), m))
		}
		for i, a := range vals {
			b := vals[(i*7+3)%len(vals)]
			for op := 0; op <= 11; op++ {
				cases = append(cases, c{f, op, a, b})
				lines = append(lines, fmt.Sprintf("%d %d %s %s", f, op, cr2LE(a), cr2LE(b)))
			}
		}
	}
	src := cryptoPrelude + strings.TrimPrefix(cr2FiatMain, "import std.crypto\n")
	for _, v := range cr2Variants[:2] {
		dir, err := packageTempDir("mkcrypto2fiat")
		if err != nil {
			t.Fatal(err)
		}
		exe := cr2BuildIn(t, dir, v, src, map[string]string{"crypto/tests_crypto2fiat.mk": cr2FiatFixture}, nil)
		r := runNative(t, exe, strings.Join(lines, "\n")+"\n")
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", v.name, r.code, r.stderr)
		}
		got := strings.Split(strings.TrimRight(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(got) != len(cases) {
			t.Fatalf("%s: %d answers to %d cases", v.name, len(got), len(cases))
		}
		bad := 0
		for i, cs := range cases {
			want := cr2FiatWant(cs.f, cs.op, cs.a, cs.b)
			if want == nil {
				if got[i] == strings.Repeat("0", 96) {
					t.Errorf("%s field %d Nonzero(%x) = 0", v.name, cs.f, cs.a)
				}
				continue
			}
			if w := cr2LE(want); got[i] != w {
				t.Errorf("%s field %d op %d a %x b %x:\n got  %s\n want %s", v.name, cs.f, cs.op, cs.a, cs.b, got[i], w)
				if bad++; bad == 10 {
					t.FailNow()
				}
			}
		}
	}
}
