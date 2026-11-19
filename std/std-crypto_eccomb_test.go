// Why: EcSecret.Public and EcdsaSign multiply the base point through P-256's
// precomputed comb, a separate code path from the generic multiplication.
// What: TestSTDCRYPTO_EcBaseMulMatchesGo, P256PtBaseMul against Go.
// How: the EC driver's B command, compared with Go's base multiplication.

package std

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// TestSTDCRYPTO_EcBaseMulMatchesGo: k G by the comb against
// crypto/elliptic for k of 0, 1, 2, n - 1, n, n + 1, all-ones bytes, every
// single bit at the comb's tooth and position boundaries (0, 31, 32, 63,
// 64, ..., 255), the patterns that fill one comb window (bits i, i + 64,
// i + 128 and i + 192, with and without i + 32's), and 40 random scalars.
func TestSTDCRYPTO_EcBaseMulMatchesGo(t *testing.T) {
	t.Parallel()
	cv := cr2EcCurveOf(0)
	n := cv.Params().N
	scalar := func(v *big.Int) []byte { return v.FillBytes(make([]byte, 32)) }
	bit := func(bits ...int) []byte {
		v := new(big.Int)
		for _, b := range bits {
			v.SetBit(v, b, 1)
		}
		return scalar(v)
	}
	one := big.NewInt(1)
	ks := [][]byte{scalar(big.NewInt(0)), scalar(one), scalar(big.NewInt(2)), scalar(new(big.Int).Sub(n, one)),
		scalar(n), scalar(new(big.Int).Add(n, one)), []byte(strings.Repeat("\xff", 32))}
	for _, b := range []int{0, 1, 31, 32, 33, 63, 64, 95, 96, 127, 128, 159, 160, 191, 192, 223, 224, 254, 255} {
		ks = append(ks, bit(b))
	}
	for _, i := range []int{0, 7, 31} {
		ks = append(ks, bit(i, i+64, i+128, i+192), bit(i, i+32, i+64, i+96, i+128, i+160, i+192, i+224), bit(i+64, i+192))
	}
	rnd := cryptoSplitmix{s: 2026}
	for range 40 {
		k := make([]byte, 32)
		rnd.fill(k)
		ks = append(ks, k)
	}
	gx, gy := cv.Params().Gx, cv.Params().Gy
	var lines, want []string
	for _, k := range ks {
		rx, ry := cr2EcMul(cv, gx, gy, k)
		lines = append(lines, fmt.Sprintf("B %x", k))
		want = append(want, "B "+cr2EcPoint(cv, rx, ry))
	}
	cr2EcCheck(t, "comb base mult", lines, want)
}
