// Why: ExpF32's kernel is written out in ExpF32 (trans32.mk) so it is not a
// call, and the copy must not drift from ExpCore (exptab.mk), the kernel it
// repeats.
// What: one test block: over every 4,096th f32 of the fast range, both signs,
// ExpF32 gives the bits of Cast[f32](ExpCore(x, 0.0)).
// How: a stride over the bit patterns; TestSTDMATH_Exhaustive pins ExpF32 and
// LogF32 over all 2^32 inputs.

test "ExpF32's written-out kernel gives ExpCore's bits" {
    // 2^-26 <= |x| < 88: bits 0x32800000 to 0x42B00000, either sign.
    var b: u32 = 0x32800000
    for b < 0x42B00000 {
        for s in 0..2 {
            x := f32.FromBits(b | Trunc[u32](Cast[u64](s)) << 31)
            Assert(ExpF32(x).Bits() == Cast[f32](ExpCore(f64(x), 0.0)).Bits())
        }
        b = b + 4096
    }
}
