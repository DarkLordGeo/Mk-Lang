// Why: std.raw's carry intrinsics (std/raw/carry.mk) back multi-word
// arithmetic, which must give the same bits at compile time as natively.
//
// What: carryAdd, carrySub and carryIntrinsic, registered in init as
// AddCarry64 and SubBorrow64.
//
// How: each runs its definition: AddCarry64(x, y, c) is
// x + y + c with carry 1 when that is 2^64 or more, SubBorrow64(x, y, b)
// is x - y - b modulo 2^64 with borrow 1 when x < y + b. Each is two
// wrapping steps whose flags are or-ed, which is codegen's lowering, so a
// carry in above 1 gives the same bits here as in a binary.

package eval

import (
	"math/bits"

	"mk/internal/mir"
	"mk/internal/types"
)

// carryAdd is AddCarry64's definition.
func carryAdd(x, y, c uint64) (sum, carry uint64) {
	s, o1 := bits.Add64(x, y, 0)
	s, o2 := bits.Add64(s, c, 0)
	return s, o1 | o2
}

// carrySub is SubBorrow64's definition.
func carrySub(x, y, b uint64) (diff, borrow uint64) {
	d, o1 := bits.Sub64(x, y, 0)
	d, o2 := bits.Sub64(d, b, 0)
	return d, o1 | o2
}

// carryIntrinsic runs op over the three arguments and stores its value in
// the result field named val and its flag in the one named flag.
func carryIntrinsic(op func(x, y, c uint64) (uint64, uint64), val string) intrinsicFunc {
	return func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		v, fl := op(m.argU(f, t, 0), m.argU(f, t, 1), m.argU(f, t, 2))
		dst := m.dest(f, t)
		for i, fd := range dst.ti.ty.Fields {
			x := fl
			if m.cfg.Interner.Str(fd.Name) == val {
				x = v
			}
			lf := dst.ti.layout.Fields[i]
			m.store(m.at(dst, lf.Offset, lf.Type), x)
		}
	}
}

func init() {
	intrinsics["std.raw.AddCarry64"] = carryIntrinsic(carryAdd, "sum")
	intrinsics["std.raw.SubBorrow64"] = carryIntrinsic(carrySub, "diff")
}
