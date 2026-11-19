// Why: gamma correction needs a power function std.math does not have.
// What: GammaScalar, GammaN, GammaAt, Log2 and Exp2.
// How: log2 and 2^z built from f32's representation, as described below.

import std.math
import std.simd

// The gamma table: sample v maps to maxval * (v / maxval)^(1 / g), rounded
// half up. std.math has no power, logarithm or exponential, so the table is
// computed from the representation of f32: log2 from the exponent field and
// a series in the mantissa, 2^z from an exponent field built for floor(z)
// times a series in the fraction. The series are accurate to about 1e-5,
// well inside the half step an 8-bit sample rounds by. The scalar body reads
// and builds the bits with f32.Bits and f32.FromBits, the vector body with
// Reinterpret; both run the same operations in the same order, so every path
// gives the same table.

/// 2 / ln 2 and its thirds, fifths and sevenths: log2(m) is
/// 2 / ln 2 * (t + t^3 / 3 + t^5 / 5 + t^7 / 7 + ...) for t = (m - 1) / (m + 1).
const K1: f32 = 2.8853900817779268
const K3: f32 = 0.9617966939259756
const K5: f32 = 0.5770780163555854
const K7: f32 = 0.41219858311113243

/// ln 2, and 1 / k! for k = 2 to 6: 2^f = e^(f ln 2).
const Ln2: f32 = 0.6931471805599453
const E2: f32 = 0.5
const E3: f32 = 0.16666666666666666
const E4: f32 = 0.041666666666666664
const E5: f32 = 0.008333333333333333
const E6: f32 = 0.001388888888888889

/// log2(x) for a normal positive `x`. O(1).
Log2(x: f32): f32 {
    bits := x.Bits()
    e := Cast[f32](Cast[i32](bits >> 23) - 127)
    m := f32.FromBits((bits & 0x7FFFFF) | 0x3F800000)
    t := (m - 1.0) / (m + 1.0)
    t2 := t * t
    return e + t * (K1 + t2 * (K3 + t2 * (K5 + t2 * K7)))
}

/// 2^z for `z` from -126 to 127. O(1).
Exp2(z: f32): f32 {
    t := Cast[f32](Cast[i32](z))
    k := if t > z { t - 1.0 } else { t }
    u := (z - k) * Ln2
    p := 1.0 + u * (1.0 + u * (E2 + u * (E3 + u * (E4 + u * (E5 + u * E6)))))
    return p * f32.FromBits(Cast[u32](Cast[i32](k) + 127) << 23)
}

/// The table entry for `v`: 0 for 0, maxval past maxval. O(1).
GammaAt(v: int, inverse: f32, maxval: int): u8 {
    if v == 0 {
        return 0
    }
    top := Cast[f32](maxval)
    x := Cast[f32](math.Min(v, maxval)) / top
    y := Exp2(Log2(x) * inverse)
    return Trunc[u8](math.Min(Cast[int](y * top + 0.5), maxval))
}

/// Fills `lut`, 256 entries, for the exponent `inverse`, 1 / g, from 0.1
/// to 10. O(256).
export GammaScalar(inverse: f32, maxval: int, lut: mut []u8) {
    for v in 0..256 {
        lut[v] = GammaAt(v, inverse, maxval)
    }
}

/// The same table N entries a step in f32 lanes, the fields of each lane
/// read and built through Reinterpret to u32 or from i32 lanes. Lane 0 is
/// computed at 1 and then replaced by 0 under the mask of the zero entry,
/// cast from the i32 comparison to the f32 lanes. O(256 / N).
GammaN[const N: int](inverse: f32, maxval: int, lut: mut []u8)
    requires N == 4 || N == 8 || N == 16
{
    one := simd.Vec[f32, N].Splat(1.0)
    top := simd.Vec[f32, N].Splat(Cast[f32](maxval))
    zero := simd.Vec[i32, N].Zero()
    lane := simd.Vec[i32, N].Iota()
    fraction := simd.Vec[u32, N].Splat(0x7FFFFF)
    exponent_one := simd.Vec[u32, N].Splat(0x3F800000)
    bias := simd.Vec[i32, N].Splat(127)
    for v0 in 0..256 / N {
        v := simd.Vec[i32, N].Splat(Trunc[i32](v0 * N)) +% lane
        empty := v == zero
        x := empty.Cast[f32]().Select(one, v.Cast[f32]().Min(top) / top)
        bits := x.Reinterpret[u32, N]()
        e := ((bits >> 23).Cast[i32]() -% bias).Cast[f32]()
        m := ((bits & fraction) | exponent_one).Reinterpret[f32, N]()
        t := (m - one) / (m + one)
        t2 := t * t
        k1 := simd.Vec[f32, N].Splat(K1)
        k3 := simd.Vec[f32, N].Splat(K3)
        k5 := simd.Vec[f32, N].Splat(K5)
        k7 := simd.Vec[f32, N].Splat(K7)
        z := (e + t * (k1 + t2 * (k3 + t2 * (k5 + t2 * k7)))) * simd.Vec[f32, N].Splat(inverse)
        k := z.Floor()
        u := (z - k) * simd.Vec[f32, N].Splat(Ln2)
        e2 := simd.Vec[f32, N].Splat(E2)
        e3 := simd.Vec[f32, N].Splat(E3)
        e4 := simd.Vec[f32, N].Splat(E4)
        e5 := simd.Vec[f32, N].Splat(E5)
        e6 := simd.Vec[f32, N].Splat(E6)
        p := one + u * (one + u * (e2 + u * (e3 + u * (e4 + u * (e5 + u * e6)))))
        scale := ((k.Cast[i32]() +% bias) << 23).Reinterpret[f32, N]()
        y := p * scale
        out := (y * top + simd.Vec[f32, N].Splat(0.5)).RoundToZero().Cast[i32]().Min(simd.Vec[i32, N].Splat(Trunc[i32](maxval)))
        empty.Select(zero, out).Trunc[u8]().CopyTo(lut, v0 * N)
    }
}
