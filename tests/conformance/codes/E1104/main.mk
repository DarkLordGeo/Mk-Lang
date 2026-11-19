// verdict: reject E1104
// note: asm reaches memory through raw pointers, so a slice is no operand; pkg.mk lists k in unsafe_modules
import k

First(xs: []u8): u8 {
    return k.FirstByte(xs)
}
