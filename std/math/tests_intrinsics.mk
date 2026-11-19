// Why: the arithmetic intrinsics (intrinsics.mk) need known answers.
// What: tests of the overflow flags, ToInt32 and ToUint32, the saturating
// casts and the wide operations, run by `mk test --std=std.math`.
// How: every value is a const, so the evaluator computes it at compile
// time and the run only compares with Go's (math/big) expected values.

const AddI8: (value: i8, overflow: bool) = AddOverflow[i8](100, 100)
const AddU64: (value: u64, overflow: bool) = AddOverflow[u64](18446744073709551615, 1)
const AddInt: (value: int, overflow: bool) = AddOverflow[int](-5, 3)
const SubU8: (value: u8, overflow: bool) = SubOverflow[u8](1, 2)
const SubI64: (value: i64, overflow: bool) = SubOverflow[i64](-9223372036854775807, 2)
const MulI16: (value: i16, overflow: bool) = MulOverflow[i16](-32768, -1)
const MulU32: (value: u32, overflow: bool) = MulOverflow[u32](65536, 65535)
const WrapA: i32 = WrapI32(4294967297.5)
const WrapB: i32 = WrapI32(2147483648.0)
const WrapC: u32 = WrapU32(-1.0)
const WrapD: u32 = WrapU32(Inf)
const WrapE: i32 = WrapI32(Nan)
const SatA: u8 = CastSat[u8](300.0)
const SatB: i8 = CastSat[i8](-1000.5)
const SatC: i64 = CastSat[i64](Nan)
const SatD: u16 = CastSatF32[u16](-3.5)
const SatE: int = CastSatF32[int](12.75)
const Wide: (hi: u64, lo: u64) = MulWide(18364758544493064720, 81985529216486895)
const Div: (q: u64, r: u64) = DivWide(81985529216486895, 18364758544493064720, 18364758544493064721)
const Carry: (sum: u64, carry: u64) = AddCarry(18446744073709551615, 1, 1)
const Borrow: (diff: u64, borrow: u64) = SubBorrow(0, 0, 1)

test "overflow-flag arithmetic" {
    Assert(AddI8.value == -56 && AddI8.overflow)
    Assert(AddU64.value == 0 && AddU64.overflow)
    Assert(AddInt.value == -2 && !AddInt.overflow)
    Assert(SubU8.value == 255 && SubU8.overflow)
    Assert(SubI64.value == 9223372036854775807 && SubI64.overflow)
    Assert(MulI16.value == -32768 && MulI16.overflow)
    Assert(MulU32.value == 4294901760 && !MulU32.overflow)
}

test "ECMA-262 ToInt32 and ToUint32" {
    Assert(WrapA == 1)
    Assert(WrapB == -2147483648)
    Assert(WrapC == 4294967295)
    Assert(WrapD == 0 && WrapE == 0)
}

test "saturating casts" {
    Assert(SatA == 255 && SatB == -128 && SatC == 0 && SatD == 0 && SatE == 12)
}

test "wide operations" {
    Assert(Wide.hi == 0x121fa00ad77d742 && Wide.lo == 0x2236d88fe5618cf0)
    Assert(Div.q == 0x124924924924924 && Div.r == 0x7d11b781de844eac)
    Assert(Carry.sum == 1 && Carry.carry == 1)
    Assert(Borrow.diff == 18446744073709551615 && Borrow.borrow == 1)
}
