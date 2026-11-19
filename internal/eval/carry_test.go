// Why: the carry intrinsics of std/raw/carry.mk must mean the same in the
// evaluator as in a compiled binary, including at the edges of u64.
// What: TestEvalCarry, over AddCarry64 and SubBorrow64.
// How: compares eval with math/bits for a carry in of 0 or 1 and with exact
// big-integer arithmetic for every carry in, on edge and random limbs.

package eval

import (
	"math/big"
	"math/bits"
	"math/rand"
	"testing"
)

// carryEdges are the limb values the carry tests cross.
var carryEdges = []uint64{0, 1, 2, 1<<63 - 1, 1 << 63, 1<<63 + 1, ^uint64(0) - 1, ^uint64(0)}

func TestEvalCarry(t *testing.T) {
	two64 := new(big.Int).Lsh(big.NewInt(1), 64)
	check := func(x, y, c uint64) {
		bx, by, bc := new(big.Int).SetUint64(x), new(big.Int).SetUint64(y), new(big.Int).SetUint64(c)
		sum := new(big.Int).Add(new(big.Int).Add(bx, by), bc)
		wantCarry := uint64(0)
		if sum.Cmp(two64) >= 0 {
			wantCarry = 1
		}
		wantSum := new(big.Int).Mod(sum, two64).Uint64()
		if s, k := carryAdd(x, y, c); s != wantSum || k != wantCarry {
			t.Fatalf("AddCarry64(%#x, %#x, %#x) = (%#x, %d), want (%#x, %d)", x, y, c, s, k, wantSum, wantCarry)
		}
		diff := new(big.Int).Sub(bx, new(big.Int).Add(by, bc))
		wantBorrow := uint64(0)
		if diff.Sign() < 0 {
			wantBorrow = 1
		}
		wantDiff := new(big.Int).Mod(diff, two64).Uint64()
		if d, b := carrySub(x, y, c); d != wantDiff || b != wantBorrow {
			t.Fatalf("SubBorrow64(%#x, %#x, %#x) = (%#x, %d), want (%#x, %d)", x, y, c, d, b, wantDiff, wantBorrow)
		}
		if c <= 1 {
			s, k := bits.Add64(x, y, c)
			d, b := bits.Sub64(x, y, c)
			if gs, gk := carryAdd(x, y, c); gs != s || gk != k {
				t.Fatalf("AddCarry64(%#x, %#x, %d) differs from bits.Add64", x, y, c)
			}
			if gd, gb := carrySub(x, y, c); gd != d || gb != b {
				t.Fatalf("SubBorrow64(%#x, %#x, %d) differs from bits.Sub64", x, y, c)
			}
		}
	}
	for _, x := range carryEdges {
		for _, y := range carryEdges {
			for _, c := range carryEdges {
				check(x, y, c)
			}
		}
	}
	rng := rand.New(rand.NewSource(590))
	for i := 0; i < 20000; i++ {
		c := rng.Uint64() & 1
		if i%4 == 0 {
			c = rng.Uint64()
		}
		check(rng.Uint64(), rng.Uint64(), c)
	}
	for _, key := range []string{"std.raw.AddCarry64", "std.raw.SubBorrow64"} {
		if _, ok := intrinsics[key]; !ok {
			t.Errorf("eval has no intrinsic %s", key)
		}
	}
}
