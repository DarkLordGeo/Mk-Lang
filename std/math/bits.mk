// Why: bit counts, reversals, Gcd and checked powers are small, exact
// integer operations every program needs, and each should be one
// instruction where the target has it.
//
// What: LeadingZeros, TrailingZeros, OnesCount, ReverseBits, ReverseBytes
// and the rotations at 8, 16, 32 and 64 bits, Gcd, CheckedPow and
// CheckedPowU64. Each takes the unsigned type of its width; a signed value
// goes through Cast.
//
// How: the counts are compiler intrinsics (lzcnt, tzcnt, popcnt on x86_64
// at the right cpu level), the reversals are the classic swap networks
// (Hacker's Delight 7-1), Gcd is Stein's binary algorithm with trailing-zero
// counts, and the powers square and multiply with every product checked
// through the 128-bit product. Nothing allocates and nothing panics.

import std.raw

/// LeadingZeros64 counts the zero bits above x's highest one; 64 for 0.
/// O(1); allocates nothing.
export LeadingZeros64(x: u64): int {
    return raw.Clz64(x)
}

/// LeadingZeros32 counts the zero bits above x's highest one; 32 for 0.
/// O(1); allocates nothing.
export LeadingZeros32(x: u32): int {
    return raw.Clz64(u64(x)) - 32
}

/// LeadingZeros16 counts the zero bits above x's highest one; 16 for 0.
/// O(1); allocates nothing.
export LeadingZeros16(x: u16): int {
    return raw.Clz64(u64(x)) - 48
}

/// LeadingZeros8 counts the zero bits above x's highest one; 8 for 0.
/// O(1); allocates nothing.
export LeadingZeros8(x: u8): int {
    return raw.Clz64(u64(x)) - 56
}

/// TrailingZeros64 counts the zero bits below x's lowest one; 64 for 0.
/// O(1); allocates nothing.
export TrailingZeros64(x: u64): int {
    return raw.Ctz64(x)
}

/// TrailingZeros32 counts the zero bits below x's lowest one; 32 for 0.
/// O(1); allocates nothing.
export TrailingZeros32(x: u32): int {
    return raw.Ctz64(u64(x) | 0x100000000)
}

/// TrailingZeros16 counts the zero bits below x's lowest one; 16 for 0.
/// O(1); allocates nothing.
export TrailingZeros16(x: u16): int {
    return raw.Ctz64(u64(x) | 0x10000)
}

/// TrailingZeros8 counts the zero bits below x's lowest one; 8 for 0.
/// O(1); allocates nothing.
export TrailingZeros8(x: u8): int {
    return raw.Ctz64(u64(x) | 0x100)
}

/// OnesCount64 counts x's one bits. O(1); allocates nothing.
export OnesCount64(x: u64): int {
    return raw.Popcount64(x)
}

/// OnesCount32 counts x's one bits. O(1); allocates nothing.
export OnesCount32(x: u32): int {
    return raw.Popcount64(u64(x))
}

/// OnesCount16 counts x's one bits. O(1); allocates nothing.
export OnesCount16(x: u16): int {
    return raw.Popcount64(u64(x))
}

/// OnesCount8 counts x's one bits. O(1); allocates nothing.
export OnesCount8(x: u8): int {
    return raw.Popcount64(u64(x))
}

/// RotateLeft64 rotates x left by n mod 64 bits; a negative n rotates
/// right. O(1); allocates nothing.
export RotateLeft64(x: u64, n: int): u64 {
    return raw.RotL64(x, n & 63)
}

/// RotateLeft32 rotates x left by n mod 32 bits; a negative n rotates
/// right. O(1); allocates nothing.
export RotateLeft32(x: u32, n: int): u32 {
    k := n & 31
    if k == 0 {
        return x
    }
    return x << k | x >> (32 - k)
}

/// RotateLeft16 rotates x left by n mod 16 bits; a negative n rotates
/// right. O(1); allocates nothing.
export RotateLeft16(x: u16, n: int): u16 {
    k := n & 15
    if k == 0 {
        return x
    }
    return x << k | x >> (16 - k)
}

/// RotateLeft8 rotates x left by n mod 8 bits; a negative n rotates right.
/// O(1); allocates nothing.
export RotateLeft8(x: u8, n: int): u8 {
    k := n & 7
    if k == 0 {
        return x
    }
    return x << k | x >> (8 - k)
}

/// RotateRight64 rotates x right by n mod 64 bits; a negative n rotates
/// left. O(1); allocates nothing.
export RotateRight64(x: u64, n: int): u64 {
    return raw.RotL64(x, (64 - (n & 63)) & 63)
}

/// RotateRight32 rotates x right by n mod 32 bits; a negative n rotates
/// left. O(1); allocates nothing.
export RotateRight32(x: u32, n: int): u32 {
    return RotateLeft32(x, 32 - (n & 31))
}

/// RotateRight16 rotates x right by n mod 16 bits; a negative n rotates
/// left. O(1); allocates nothing.
export RotateRight16(x: u16, n: int): u16 {
    return RotateLeft16(x, 16 - (n & 15))
}

/// RotateRight8 rotates x right by n mod 8 bits; a negative n rotates
/// left. O(1); allocates nothing.
export RotateRight8(x: u8, n: int): u8 {
    return RotateLeft8(x, 8 - (n & 7))
}

/// ReverseBytes64 reverses the order of x's bytes. O(1); allocates nothing.
export ReverseBytes64(x: u64): u64 {
    var y := (x >> 8) & 0x00FF00FF00FF00FF | (x & 0x00FF00FF00FF00FF) << 8
    y = (y >> 16) & 0x0000FFFF0000FFFF | (y & 0x0000FFFF0000FFFF) << 16
    return y >> 32 | y << 32
}

/// ReverseBytes32 reverses the order of x's bytes. O(1); allocates nothing.
export ReverseBytes32(x: u32): u32 {
    y := (x >> 8) & 0x00FF00FF | (x & 0x00FF00FF) << 8
    return y >> 16 | y << 16
}

/// ReverseBytes16 swaps x's two bytes. O(1); allocates nothing.
export ReverseBytes16(x: u16): u16 {
    return x >> 8 | x << 8
}

/// ReverseBits64 reverses the order of x's bits. O(1); allocates nothing.
export ReverseBits64(x: u64): u64 {
    var y := (x >> 1) & 0x5555555555555555 | (x & 0x5555555555555555) << 1
    y = (y >> 2) & 0x3333333333333333 | (y & 0x3333333333333333) << 2
    y = (y >> 4) & 0x0F0F0F0F0F0F0F0F | (y & 0x0F0F0F0F0F0F0F0F) << 4
    return ReverseBytes64(y)
}

/// ReverseBits32 reverses the order of x's bits. O(1); allocates nothing.
export ReverseBits32(x: u32): u32 {
    var y := (x >> 1) & 0x55555555 | (x & 0x55555555) << 1
    y = (y >> 2) & 0x33333333 | (y & 0x33333333) << 2
    y = (y >> 4) & 0x0F0F0F0F | (y & 0x0F0F0F0F) << 4
    return ReverseBytes32(y)
}

/// ReverseBits16 reverses the order of x's bits. O(1); allocates nothing.
export ReverseBits16(x: u16): u16 {
    return Trunc[u16](ReverseBits32(u32(x)) >> 16)
}

/// ReverseBits8 reverses the order of x's bits. O(1); allocates nothing.
export ReverseBits8(x: u8): u8 {
    return Trunc[u8](ReverseBits32(u32(x)) >> 24)
}

/// Gcd is the greatest common divisor of a and b; Gcd(a, 0) is a, so
/// Gcd(0, 0) is 0. Stein's binary algorithm: O(log a + log b) steps of a
/// subtraction and a shift, no division; allocates nothing.
export Gcd(a: u64, b: u64): u64 {
    if a == 0 {
        return b
    }
    if b == 0 {
        return a
    }
    shift := raw.Ctz64(a | b)
    var u := a >> raw.Ctz64(a)
    var v := b
    for {
        v = v >> raw.Ctz64(v)
        if u > v {
            t := u // swapped by hand: std.math is layer 0, std.core.Swap layer 1
            u = v
            v = t
        }
        v -= u
        if v == 0 {
            return u << shift
        }
    }
}

/// CheckedPowU64 is x to the power n, or none when it exceeds the u64
/// range; x^0 is 1, 0^0 included. O(log n) multiplications; allocates
/// nothing.
export CheckedPowU64(x: u64, n: u32): ?u64 {
    var r: u64 = 1
    var b := x
    var e := n
    for {
        if e & 1 == 1 {
            p := raw.Mul128(r, b)
            if p.hi != 0 {
                return none
            }
            r = p.lo
        }
        e = e >> 1
        if e == 0 {
            return some(r)
        }
        // b squared is a factor of the result from here on, and r is not
        // 0 unless x is, so an overflow here is the result's.
        q := raw.Mul128(b, b)
        if q.hi != 0 {
            return none
        }
        b = q.lo
    }
}

/// CheckedPow is x to the power n, or none when it leaves the i64 range;
/// x^0 is 1, 0^0 included. O(log n) multiplications; allocates nothing.
export CheckedPow(x: i64, n: u32): ?i64 {
    // |x| as u64; -(x + 1) + 1 cannot overflow at the least i64.
    mag := if x < 0 { Cast[u64](-(x + 1)) + 1 } else { Cast[u64](x) }
    m := CheckedPowU64(mag, n) else { return none }
    if x < 0 && n & 1 == 1 {
        if m > 0x8000000000000000 {
            return none
        }
        // -m, through its complement, reaches the least i64 too.
        return some(Trunc[i64](~m +% 1))
    }
    if m > 0x7FFFFFFFFFFFFFFF {
        return none
    }
    return some(Trunc[i64](m))
}
