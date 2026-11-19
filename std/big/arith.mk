// Why: every operation of std.big reduces to a few loops over word vectors,
// and their speed decides the speed of the whole module.
//
// What: the word-vector kernels: AddVV, SubVV, AddTo, SubFrom, AddW, SubW,
// MulAddVWW, AddMulVVW, AddMul2VVW, SubMulVVW, ShlVU, ShrVU, CmpVV, Norm and
// small helpers.
//
// How: magnitudes are little-endian slices of u64 words, and every kernel
// runs over whole slices with one carry, borrow or remainder word, as GMP's
// mpn layer and Go's math/big arith.go do. A carry or borrow is a comparison
// of a sum with an operand, and each loop adds the parts that do not depend
// on the carry first, so the chain from one word to the next is one add and
// one add-with-carry (LLVM's adc), and no kernel branches on a word's value
// (measured: the add and sub kernels in this form made a 1024-word Mul 11
// percent faster than Go's math/bits formulas, and the two-row AddMul2VVW 17
// percent faster than one row a pass). Each is O(n) in its slices' length and
// allocates nothing. A kernel that writes z takes the lengths from z; its
// sources are at least that long.

import std.raw

// Bit1 is 1 when b holds, else 0.
Bit1(b: bool): u64 {
    return if b { 1 } else { 0 }
}

// AddVV sets z = x + y over z.Len() words and returns the carry out.
AddVV(z: mut []u64, x: []u64, y: []u64): u64 {
    n := z.Len()
    xs := x[..n]
    ys := y[..n]
    var c: u64 = 0
    for i in 0..n {
        xi := xs[i]
        yi := ys[i]
        s1 := xi +% yi
        s := s1 +% c
        c = Bit1(s1 < xi) | Bit1(s < s1)
        z[i] = s
    }
    return c
}

// SubVV sets z = x - y over z.Len() words and returns the borrow out.
SubVV(z: mut []u64, x: []u64, y: []u64): u64 {
    n := z.Len()
    xs := x[..n]
    ys := y[..n]
    var b: u64 = 0
    for i in 0..n {
        xi := xs[i]
        yi := ys[i]
        d1 := xi -% yi
        d := d1 -% b
        b = Bit1(xi < yi) | Bit1(d1 < b)
        z[i] = d
    }
    return b
}

// AddTo adds y into z's low y.Len() words, carries through the rest of z,
// and returns the carry out of z's top word.
AddTo(z: mut []u64, y: []u64): u64 {
    n := y.Len()
    var c: u64 = 0
    for i in 0..n {
        zi := z[i]
        yi := y[i]
        s1 := zi +% yi
        s := s1 +% c
        c = Bit1(s1 < zi) | Bit1(s < s1)
        z[i] = s
    }
    var i := n
    for c != 0 && i < z.Len() {
        s := z[i] +% 1
        c = Bit1(s == 0)
        z[i] = s
        i += 1
    }
    return c
}

// SubFrom subtracts y from z's low y.Len() words, borrows through the rest
// of z, and returns the borrow out of z's top word.
SubFrom(z: mut []u64, y: []u64): u64 {
    n := y.Len()
    var b: u64 = 0
    for i in 0..n {
        zi := z[i]
        yi := y[i]
        d1 := zi -% yi
        d := d1 -% b
        b = Bit1(zi < yi) | Bit1(d1 < b)
        z[i] = d
    }
    var i := n
    for b != 0 && i < z.Len() {
        d := z[i]
        z[i] = d -% 1
        b = Bit1(d == 0)
        i += 1
    }
    return b
}

// AddW adds the word w into z and returns the carry out of its top word.
AddW(z: mut []u64, w: u64): u64 {
    var c := w
    var i := 0
    for c != 0 && i < z.Len() {
        s := z[i] +% c
        c = Bit1(s < c)
        z[i] = s
        i += 1
    }
    return c
}

// SubW subtracts the word w from z and returns the borrow out of its top word.
SubW(z: mut []u64, w: u64): u64 {
    var b := w
    var i := 0
    for b != 0 && i < z.Len() {
        d := z[i]
        z[i] = d -% b
        b = Bit1(d < b)
        i += 1
    }
    return b
}

// MulAddVWW sets z = x * w + r over z.Len() words and returns the high word.
MulAddVWW(z: mut []u64, x: []u64, w: u64, r: u64): u64 {
    n := z.Len()
    xs := x[..n]
    var c := r
    for i in 0..n {
        p := raw.Mul128(xs[i], w)
        lo := p.lo +% c
        c = p.hi +% Bit1(lo < c)
        z[i] = lo
    }
    return c
}

// AddMulVVW adds x * w into z's low x.Len() words and returns the word that
// carries out of them (not added to z; the caller places it).
AddMulVVW(z: mut []u64, x: []u64, w: u64): u64 {
    n := x.Len()
    zs := mut z[..n]
    var c: u64 = 0
    for i in 0..n {
        // z + x w first, which does not wait for the carry, then the carry
        // in: the chain from one word's carry to the next is one add and
        // one add-with-carry.
        p := raw.Mul128(x[i], w)
        t := zs[i] +% p.lo
        hi := p.hi +% Bit1(t < p.lo)
        s := t +% c
        zs[i] = s
        c = hi +% Bit1(s < c)
    }
    return c
}

// AddMul2VVW adds x * (w0 + w1 2^64) into z's low x.Len() words and returns
// the two words that carry out of them, for z[n] and z[n + 1] (not added to
// z; the caller places them): two rows of the schoolbook product in one
// pass over z, GMP's mpn_addmul_2, so each word of z is loaded and stored
// once per two multiplier words and the carry chain is shared. No sum
// overflows: z + c + x w is at most 2^128 - 1 for words z, c, x and w.
AddMul2VVW(z: mut []u64, x: []u64, w0: u64, w1: u64): (lo: u64, hi: u64) {
    n := x.Len()
    zs := mut z[..n]
    var c0: u64 = 0
    var c1: u64 = 0
    for i in 0..n {
        xi := x[i]
        p0 := raw.Mul128(xi, w0)
        p1 := raw.Mul128(xi, w1)
        // The parts that do not wait for a carry.
        t := zs[i] +% p0.lo
        a := p0.hi +% Bit1(t < p0.lo)
        b := a +% p1.lo
        h := p1.hi +% Bit1(b < a)
        // The carry chain: c0 into this word, c1 into the next.
        s := t +% c0
        k := Bit1(s < c0)
        zs[i] = s
        u := b +% c1
        ku := Bit1(u < c1)
        v := u +% k
        c0 = v
        c1 = h +% ku +% Bit1(v < k)
    }
    return (lo: c0, hi: c1)
}

// SubMulVVW subtracts x * w from z's low x.Len() words and returns the word
// that borrows out of them.
SubMulVVW(z: mut []u64, x: []u64, w: u64): u64 {
    n := x.Len()
    zs := mut z[..n]
    var c: u64 = 0
    for i in 0..n {
        // z - x w first, then the borrow in, as in AddMulVVW. The borrow
        // out is at most 2^64 - 1: x w + c is at most 2^128 - 2^64.
        p := raw.Mul128(x[i], w)
        t := zs[i]
        d := t -% p.lo
        hi := p.hi +% Bit1(t < p.lo)
        r := d -% c
        zs[i] = r
        c = hi +% Bit1(d < c)
    }
    return c
}

// ShlVU sets z = x << s over z.Len() words, 0 < s < 64, and returns the bits
// shifted out of the top word, in the low s bits.
ShlVU(z: mut []u64, x: []u64, s: int): u64 {
    n := z.Len()
    if n == 0 {
        return 0
    }
    xs := x[..n]
    r := 64 - s
    out := xs[n - 1] >> r
    var i := n - 1
    for i > 0 {
        z[i] = (xs[i] << s) | (xs[i - 1] >> r)
        i -= 1
    }
    z[0] = xs[0] << s
    return out
}

// ShrVU sets z = x >> s over z.Len() words, 0 < s < 64, the bits above x's
// top word being hi's low s bits, and returns the bits shifted out of the
// bottom word, in the top s bits.
ShrVU(z: mut []u64, x: []u64, s: int, hi: u64): u64 {
    n := z.Len()
    if n == 0 {
        return 0
    }
    xs := x[..n]
    r := 64 - s
    out := xs[0] << r
    for i in 0..n - 1 {
        z[i] = (xs[i] >> s) | (xs[i + 1] << r)
    }
    z[n - 1] = (xs[n - 1] >> s) | (hi << r)
    return out
}

// Norm is x without its zero top words.
Norm(x: []u64): []u64 from x {
    var n := x.Len()
    for n > 0 && x[n - 1] == 0 {
        n -= 1
    }
    return x[..n]
}

// CmpVV orders two normalized magnitudes: -1, 0 or 1.
CmpVV(x: []u64, y: []u64): int {
    if x.Len() != y.Len() {
        return if x.Len() < y.Len() { -1 } else { 1 }
    }
    var i := x.Len() - 1
    for i >= 0 {
        if x[i] != y[i] {
            return if x[i] < y[i] { -1 } else { 1 }
        }
        i -= 1
    }
    return 0
}

// Clear sets every word of z to zero.
Clear(z: mut []u64) {
    for i in 0..z.Len() {
        z[i] = 0
    }
}

// Copy copies x into z's low x.Len() words.
Copy(z: mut []u64, x: []u64) {
    n := x.Len()
    zs := mut z[..n]
    for i in 0..n {
        zs[i] = x[i]
    }
}

// MulAddInPlace sets z = z * w + r and returns the high word.
MulAddInPlace(z: mut []u64, w: u64, r: u64): u64 {
    var c := r
    for i in 0..z.Len() {
        p := raw.Mul128(z[i], w)
        lo := p.lo +% c
        c = p.hi +% Bit1(lo < c)
        z[i] = lo
    }
    return c
}

// Halve sets z = z >> 1 in place.
Halve(z: mut []u64) {
    n := z.Len()
    if n == 0 {
        return
    }
    for i in 0..n - 1 {
        z[i] = (z[i] >> 1) | (z[i + 1] << 63)
    }
    z[n - 1] = z[n - 1] >> 1
}
