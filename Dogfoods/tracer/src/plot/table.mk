// Why: Math libraries disagree at the edges; a table of results makes two
// targets comparable by `diff`.
//
// What: Table mode: every function of funcs.mk, and every integer bit
// operation, over signed zeros, subnormals, the extremes, infinities, NaN and
// huge trigonometric arguments.
//
// How: Each result prints with its bits, so a difference of one bit shows.

import std.math

/// Which widths a table covers.
export enum Width {
    F64
    F32
    Both
}

// The f64 inputs. Specials are built from their bits, so no NaN or signed
// zero depends on how a compiler folds a literal.
Inputs64(a: &Arena): OutOfMemory![]f64 from a {
    var xs := List[f64].New(a)
    for b in Special64 {
        try xs.Push(f64.FromBits(b))
    }
    try xs.Push(math.Nan)
    for x in Plain64 {
        try xs.Push(x)
    }
    return xs.Done()[..]
}

// The f32 inputs, on the same plan as Inputs64.
Inputs32(a: &Arena): OutOfMemory![]f32 from a {
    var xs := List[f32].New(a)
    for b in Special32 {
        try xs.Push(f32.FromBits(b))
    }
    for x in Plain32 {
        try xs.Push(x)
    }
    return xs.Done()[..]
}

// The integer inputs of the bit operations, per width.
const Words64: []u64 = [0x0, 0x1, 0x8000000000000000, 0xFFFFFFFFFFFFFFFF, 0xF0F0F0F0F0F0F0F0, 0x0123456789ABCDEF]
const Words32: []u32 = [0x0, 0x1, 0x80000000, 0xFFFFFFFF, 0xF0F0F0F0, 0x01234567]
const Words16: []u16 = [0x0, 0x1, 0x8000, 0xFFFF, 0xF0F0, 0x0123]
const Words8: []u8 = [0x0, 0x1, 0x80, 0xFF, 0xF0, 0x2D]
const GcdPairs: []u64 = [0, 0, 0, 12, 12, 18, 1071, 462, 0x8000000000000000, 0x10000000000, 0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFE]
const Powers: [](x: i64, n: u32) = [(x: 0, n: 0), (x: 3, n: 39), (x: 3, n: 40), (x: -2, n: 63), (x: 2, n: 63), (x: -1, n: 4294967295), (x: 10, n: 18)]
const PowersU64: [](x: u64, n: u32) = [(x: 2, n: 63), (x: 2, n: 64), (x: 3, n: 40), (x: 10, n: 19), (x: 10, n: 20), (x: 0xFFFFFFFFFFFFFFFF, n: 1)]

// The special inputs by their bits: signed zeros, the smallest subnormal,
// the largest subnormal, the smallest normal, the largest finite value, the
// infinities and, for f32, the quiet NaN.
const Special64: []u64 = [0x0, 0x8000000000000000, 0x1, 0x000FFFFFFFFFFFFF, 0x0010000000000000, 0x7FEFFFFFFFFFFFFF, 0x7FF0000000000000, 0xFFF0000000000000]
const Special32: []u32 = [0x0, 0x80000000, 0x1, 0x007FFFFF, 0x00800000, 0x7F7FFFFF, 0x7F800000, 0xFF800000, 0x7FC00000]

// Ordinary inputs, with huge arguments for trigonometric reduction and the
// edges where exp overflows and underflows.
const Plain64: []f64 = [0.5, -0.5, 0.75, 1.0, -1.0, 2.0, -2.5, 3.0, 10.0, 0.1, 3.141592653589793, -3.141592653589793, 2.718281828459045, 1e4, 1e22, -1e22, 1e300, 710.0, -745.5]
const Plain32: []f32 = [0.5, -0.5, 0.75, 1.0, -1.0, 2.0, -2.5, 3.0, 10.0, 0.1, 3.1415927, -3.1415927, 2.7182817, 1e4, 1e22, -1e22, 1e38, 89.0, -104.5]

// Pairs for the functions of two arguments, each x then y.
const Pairs64: []f64 = [7.5, 2.0, -7.5, 2.0, 7.5, -2.0, 1.0, 0.0, 0.0, 0.0, 2.0, 0.5, -8.0, 3.0, -8.0, 0.3333333333333333, 1e308, 1e308, 0.5, -1074.0, 1e22, 3.0, 0.0, -1.0, 1.0, 1e300, -1e300, 1e-300, 5.0, 1e-320, 1e22, 6.283185307179586]

// Triples for fma: each x, y and z.
const Triples64: []f64 = [0.1, 10.0, -1.0, 2.0, 3.0, 4.0, 1.0000000000000002, 0.9999999999999999, -1.0, 1e308, 10.0, -1e308, 3.0, 1e-310, 0.0, 1e-160, 1e-160, 1e-320, -0.5, 4.0, 2.0]

// Exponents for ldexp, applied to each of LdExpBases.
const Exponents: []int = [0, 1, 10, 1023, 1024, -1022, -1074, -1075, -1100, 2000, -2000]
const LdExpBases: []f64 = [1.0, 0.75, -1.5, 0.0]

/// Prints the f64 table, the f32 table or both, of every function or of those
/// `only` names, to `w`. Fails only when `w` does. O(functions times inputs); allocates
/// the inputs in `a`.
export Table(a: &Arena, w: mut Writer, width: Width, only: []str): OutOfMemory|WriteError!void {
    xs := try Inputs64(a)
    ys := try Inputs32(a)
    if width != Width.F32 {
        try Table64(w, xs, only)
    }
    if width != Width.F64 {
        try Table32(w, ys, only)
    }
}

Wanted(only: []str, name: str): bool {
    return only.Len() == 0 || only.Has(&name)
}

Table64(w: mut Writer, xs: []f64, only: []str): WriteError!void {
    for u in Unaries {
        if !Wanted(only, u.name) {
            continue
        }
        for x in xs {
            y := u.f(x)
            try Print(w, "f64 {n}({x}) = {y} {b:016x}\n", (n: u.name, x, y, b: y.Bits()))
        }
    }
    for p in Predicates {
        if !Wanted(only, p.name) {
            continue
        }
        for x in xs {
            try Print(w, "f64 {n}({x}) = {y}\n", (n: p.name, x, y: p.f(x)))
        }
    }
    if Wanted(only, "frexp") {
        for x in xs {
            r := math.FrExp(x)
            try Print(w, "f64 frexp({x}) = {f} {b:016x} {e}\n", (x, f: r.frac, b: r.frac.Bits(), e: r.exp))
        }
    }
    for f in Binaries {
        if !Wanted(only, f.name) {
            continue
        }
        for i in 0..Pairs64.Len() / 2 {
            x := Pairs64[2 * i]
            y := Pairs64[2 * i + 1]
            for (u, v) in [(u: x, v: y), (u: y, v: x), (u: x, v: math.Nan), (u: -x, v: math.Inf)] {
                r := f.f(u, v)
                try Print(w, "f64 {n}({u}, {v}) = {r} {b:016x}\n", (n: f.name, u, v, r, b: r.Bits()))
            }
        }
    }
    if Wanted(only, "fma") {
        for i in 0..Triples64.Len() / 3 {
            x := Triples64[3 * i]
            y := Triples64[3 * i + 1]
            z := Triples64[3 * i + 2]
            for (u, v, s) in [(u: x, v: y, s: z), (u: -x, v: y, s: -z), (u: x, v: math.Inf, s: z), (u: x, v: y, s: math.Nan)] {
                r := math.Fma(u, v, s)
                try Print(w, "f64 fma({u}, {v}, {s}) = {r} {b:016x}\n", (u, v, s, r, b: r.Bits()))
            }
        }
    }
    if Wanted(only, "ldexp") {
        for x in LdExpBases {
            for e in Exponents {
                for k in [e, -e] {
                    r := math.LdExp(x, k)
                    try Print(w, "f64 ldexp({x}, {k}) = {r} {b:016x}\n", (x, k, r, b: r.Bits()))
                }
            }
        }
    }
}

Table32(w: mut Writer, xs: []f32, only: []str): WriteError!void {
    for u in Unaries {
        if !Wanted(only, u.name) {
            continue
        }
        for x in xs {
            y := u.g(x)
            try Print(w, "f32 {n}({x}) = {y} {b:08x}\n", (n: u.name, x, y, b: y.Bits()))
        }
    }
    for p in Predicates {
        if !Wanted(only, p.name) {
            continue
        }
        for x in xs {
            try Print(w, "f32 {n}({x}) = {y}\n", (n: p.name, x, y: p.g(x)))
        }
    }
    if Wanted(only, "frexp") {
        for x in xs {
            r := math.FrExpF32(x)
            try Print(w, "f32 frexp({x}) = {f} {b:08x} {e}\n", (x, f: r.frac, b: r.frac.Bits(), e: r.exp))
        }
    }
    for f in Binaries {
        if !Wanted(only, f.name) {
            continue
        }
        for i in 0..Pairs64.Len() / 2 {
            x := ToF32(Pairs64[2 * i])
            y := ToF32(Pairs64[2 * i + 1])
            nan := f32.FromBits(0x7FC00000)
            inf := f32.FromBits(0x7F800000)
            for (u, v) in [(u: x, v: y), (u: y, v: x), (u: x, v: nan), (u: -x, v: inf)] {
                r := f.g(u, v)
                try Print(w, "f32 {n}({u}, {v}) = {r} {b:08x}\n", (n: f.name, u, v, r, b: r.Bits()))
            }
        }
    }
    if Wanted(only, "fma") {
        for i in 0..Triples64.Len() / 3 {
            x := ToF32(Triples64[3 * i])
            y := ToF32(Triples64[3 * i + 1])
            z := ToF32(Triples64[3 * i + 2])
            for (u, v, s) in [(u: x, v: y, s: z), (u: -x, v: y, s: -z), (u: x, v: f32.FromBits(0x7F800000), s: z)] {
                r := math.FmaF32(u, v, s)
                try Print(w, "f32 fma({u}, {v}, {s}) = {r} {b:08x}\n", (u, v, s, r, b: r.Bits()))
            }
        }
    }
    if Wanted(only, "ldexp") {
        for x in LdExpBases {
            for e in Exponents {
                for k in [e, -e] {
                    r := math.LdExpF32(ToF32(x), k)
                    try Print(w, "f32 ldexp({x}, {k}) = {r} {b:08x}\n", (x, k, r, b: r.Bits()))
                }
            }
        }
    }
}

/// Prints every integer bit operation, in each width, over edge inputs, and
/// Gcd and the checked powers over edge pairs. Fails only when `w` does.
/// O(1); allocates nothing.
export Bits(w: mut Writer): WriteError!void {
    for x in Words64 {
        try Print(w, "u64 {x:016x}: lz {a} tz {b} ones {c} rotl1 {d:016x} rotl67 {e:016x} rotl-5 {f:016x} rotr1 {g:016x} rotr-5 {h:016x} rev {i:016x} bswap {j:016x}\n", (x, a: math.LeadingZeros64(x), b: math.TrailingZeros64(x), c: math.OnesCount64(x), d: math.RotateLeft64(x, 1), e: math.RotateLeft64(x, 67), f: math.RotateLeft64(x, -5), g: math.RotateRight64(x, 1), h: math.RotateRight64(x, -5), i: math.ReverseBits64(x), j: math.ReverseBytes64(x)))
    }
    for x in Words32 {
        try Print(w, "u32 {x:08x}: lz {a} tz {b} ones {c} rotl1 {d:08x} rotl35 {e:08x} rotl-5 {f:08x} rotr1 {g:08x} rotr-5 {h:08x} rev {i:08x} bswap {j:08x}\n", (x, a: math.LeadingZeros32(x), b: math.TrailingZeros32(x), c: math.OnesCount32(x), d: math.RotateLeft32(x, 1), e: math.RotateLeft32(x, 35), f: math.RotateLeft32(x, -5), g: math.RotateRight32(x, 1), h: math.RotateRight32(x, -5), i: math.ReverseBits32(x), j: math.ReverseBytes32(x)))
    }
    for x in Words16 {
        try Print(w, "u16 {x:04x}: lz {a} tz {b} ones {c} rotl1 {d:04x} rotl19 {e:04x} rotl-5 {f:04x} rotr1 {g:04x} rotr-5 {h:04x} rev {i:04x} bswap {j:04x}\n", (x, a: math.LeadingZeros16(x), b: math.TrailingZeros16(x), c: math.OnesCount16(x), d: math.RotateLeft16(x, 1), e: math.RotateLeft16(x, 19), f: math.RotateLeft16(x, -5), g: math.RotateRight16(x, 1), h: math.RotateRight16(x, -5), i: math.ReverseBits16(x), j: math.ReverseBytes16(x)))
    }
    for x in Words8 {
        try Print(w, "u8 {x:02x}: lz {a} tz {b} ones {c} rotl1 {d:02x} rotl11 {e:02x} rotl-5 {f:02x} rotr1 {g:02x} rotr-5 {h:02x} rev {i:02x}\n", (x, a: math.LeadingZeros8(x), b: math.TrailingZeros8(x), c: math.OnesCount8(x), d: math.RotateLeft8(x, 1), e: math.RotateLeft8(x, 11), f: math.RotateLeft8(x, -5), g: math.RotateRight8(x, 1), h: math.RotateRight8(x, -5), i: math.ReverseBits8(x)))
    }
    for i in 0..GcdPairs.Len() / 2 {
        x := GcdPairs[2 * i]
        y := GcdPairs[2 * i + 1]
        try Print(w, "gcd({x}, {y}) = {g}\n", (x, y, g: math.Gcd(x, y)))
    }
    for (x, n) in Powers {
        try Print(w, "pow({x}, {n}) = {p}\n", (x, n, p: math.CheckedPow(x, n)))
    }
    for (x, n) in PowersU64 {
        try Print(w, "powu({x}, {n}) = {p}\n", (x, n, p: math.CheckedPowU64(x, n)))
    }
}
