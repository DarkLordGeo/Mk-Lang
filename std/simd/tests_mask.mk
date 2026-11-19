// Why: codegen computes Mask's And, Or, Xor and Not on the mask's lane words
// (each 0 or all ones), and the results must equal the same logic on Bits().
// What: test blocks for every float and integer lane width, and for a mask
// carried around a loop (mandelvec's active lanes: And with Not of a compare,
// Any, Select) that must count what the scalar loop counts.
// How: helper names start with Tm, because every file of std/simd is one
// module.

TmRand(x: u64): u64 {
    var y := x
    y ^= y << 13
    y ^= y >> 7
    y ^= y << 17
    return y
}

// TmF64 is a double in [-2, 2) from r, with few distinct values so that
// lanes often compare equal.
TmF64(r: u64): f64 {
    return Cast[f64](Cast[i64](r >> 61) - 4) / 2.0 + Cast[f64](r & 3) * 0.25
}

TmF64x4(a: Vec[f64, 4], b: Vec[f64, 4]) {
    m := a < b
    n := a == b
    x := m.Bits()
    y := n.Bits()
    Assert((m & n).Bits() == x & y)
    Assert((m | n).Bits() == x | y)
    Assert((m ^ n).Bits() == x ^ y)
    Assert((!m).Bits() == ~x & 0xF)
    Assert((!m & !n).Bits() == ~(x | y) & 0xF)
    Assert((m & !n).Any() == (x & ~y != 0))
    Assert((m | !m).All())
}

TmF32x8(a: Vec[f32, 8], b: Vec[f32, 8]) {
    m := a <= b
    n := a > Vec[f32, 8].Zero()
    x := m.Bits()
    y := n.Bits()
    Assert((m & n).Bits() == x & y)
    Assert((m | n).Bits() == x | y)
    Assert((m ^ n).Bits() == x ^ y)
    Assert((!m).Bits() == ~x & 0xFF)
    Assert((!(m ^ n)).CountSet() + (m ^ n).CountSet() == 8)
}

TmU8x16(a: Vec[u8, 16], b: Vec[u8, 16]) {
    m := a == b
    n := a >= Vec[u8, 16].Splat(4)
    x := m.Bits()
    y := n.Bits()
    Assert((m & n).Bits() == x & y)
    Assert((m | n).Bits() == x | y)
    Assert((m ^ n).Bits() == x ^ y)
    Assert((!m).Bits() == ~x & 0xFFFF)
    if f := (m & !n).FirstSet() {
        Assert((x & ~y) >> Cast[u64](f) & 1 == 1)
    } else {
        Assert(x & ~y == 0)
    }
}

TmI16x2(a: Vec[i16, 2], b: Vec[i16, 2]) {
    m := a < b
    n := a != Vec[i16, 2].Zero()
    x := m.Bits()
    y := n.Bits()
    Assert((m & n).Bits() == x & y)
    Assert((m | n).Bits() == x | y)
    Assert((m ^ n).Bits() == x ^ y)
    Assert((!n).Bits() == ~y & 3)
}

test "masks: And, Or, Xor and Not equal the same operations on Bits, f64 x 4, f32 x 8, u8 x 16, i16 x 2" {
    var r: u64 = 0x9e3779b97f4a7c15
    for _ in 0..2000 {
        var a4: [4]f64 = [0.0; 4]
        var b4: [4]f64 = [0.0; 4]
        var a8: [8]f32 = [0.0; 8]
        var b8: [8]f32 = [0.0; 8]
        var a16: [16]u8 = [0; 16]
        var b16: [16]u8 = [0; 16]
        var a2: [2]i16 = [0; 2]
        var b2: [2]i16 = [0; 2]
        for j in 0..16 {
            r = TmRand(r)
            if j < 4 {
                a4[j] = TmF64(r)
                b4[j] = TmF64(r >> 7)
            }
            if j < 8 {
                a8[j] = Cast[f32](TmF64(r >> 3))
                b8[j] = Cast[f32](TmF64(r >> 11))
            }
            if j < 2 {
                a2[j] = Trunc[i16](Cast[i64](r >> 40) & 7) - 3
                b2[j] = Trunc[i16](Cast[i64](r >> 50) & 7) - 3
            }
            a16[j] = Trunc[u8](r >> 20) & 7
            b16[j] = Trunc[u8](r >> 30) & 7
        }
        TmF64x4(Vec[f64, 4].FromArray(a4), Vec[f64, 4].FromArray(b4))
        TmF32x8(Vec[f32, 8].FromArray(a8), Vec[f32, 8].FromArray(b8))
        TmU8x16(Vec[u8, 16].FromArray(a16), Vec[u8, 16].FromArray(b16))
        TmI16x2(Vec[i16, 2].FromArray(a2), Vec[i16, 2].FromArray(b2))
    }
}

TmPoint(cr: f64, ci: f64): int {
    var zr := 0.0
    var zi := 0.0
    var i := 0
    for i < 50 {
        tr := (zr * zr - zi * zi) + cr
        ti := (2.0 * zr) * zi + ci
        zr = tr
        zi = ti
        if zr * zr + zi * zi > 4.0 {
            break
        }
        i += 1
    }
    return i
}

// TmEscape4 is mandelvec's vector loop at 4 lanes: the escape count of
// each lane, the active mask carried from one iteration to the next.
TmEscape4(cr: Vec[f64, 4], ci: f64): [4]f64 {
    civ := Vec[f64, 4].Splat(ci)
    var zr := Vec[f64, 4].Zero()
    var zi := Vec[f64, 4].Zero()
    var cnt := Vec[f64, 4].Zero()
    var active := Mask[f64, 4].FromBits(0xF)
    for _ in 0..50 {
        tr := (zr * zr - zi * zi) + cr
        ti := (Vec[f64, 4].Splat(2.0) * zr) * zi + civ
        zr = tr
        zi = ti
        active = active & !(zr * zr + zi * zi > Vec[f64, 4].Splat(4.0))
        if !active.Any() {
            break
        }
        cnt = cnt + active.Select(Vec[f64, 4].Splat(1.0), Vec[f64, 4].Zero())
    }
    return cnt.ToArray()
}

// TmEscape2 is TmEscape4 at 2 lanes.
TmEscape2(cr: Vec[f64, 2], ci: f64): [2]f64 {
    civ := Vec[f64, 2].Splat(ci)
    var zr := Vec[f64, 2].Zero()
    var zi := Vec[f64, 2].Zero()
    var cnt := Vec[f64, 2].Zero()
    var active := Mask[f64, 2].FromBits(3)
    for _ in 0..50 {
        tr := (zr * zr - zi * zi) + cr
        ti := (Vec[f64, 2].Splat(2.0) * zr) * zi + civ
        zr = tr
        zi = ti
        active = active & !(zr * zr + zi * zi > Vec[f64, 2].Splat(4.0))
        if !active.Any() {
            break
        }
        cnt = cnt + active.Select(Vec[f64, 2].Splat(1.0), Vec[f64, 2].Zero())
    }
    return cnt.ToArray()
}

test "masks: a mask carried around a loop (And with Not, Any, Select) counts what the scalar loop counts, f64 x 4 and f64 x 2" {
    n := 48
    nf := Cast[f64](n)
    for y in 0..n {
        ci := (2.0 * Cast[f64](y)) / nf - 1.0
        var x := 0
        for x + 4 <= n {
            var crs: [4]f64 = [0.0; 4]
            for l in 0..4 {
                crs[l] = (2.0 * Cast[f64](x + l)) / nf - 1.5
            }
            c := TmEscape4(Vec[f64, 4].FromArray(crs), ci)
            c2 := TmEscape2(Vec[f64, 2].FromArray([crs[2], crs[3]]), ci)
            for l in 0..4 {
                Assert(Cast[int](c[l]) == TmPoint(crs[l], ci))
            }
            Assert(Cast[int](c2[0]) == TmPoint(crs[2], ci))
            Assert(Cast[int](c2[1]) == TmPoint(crs[3], ci))
            x += 4
        }
    }
}
