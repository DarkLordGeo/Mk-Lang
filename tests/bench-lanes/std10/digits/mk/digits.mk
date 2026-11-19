// digits, Mk: std.math.ShortestDigits (variant shortest) or
// std.math.ExactDigits(x, 17, Significant) (variant exact17) or
// ExactDigits(x, 6, Fraction) (variant fixed) over 4,096 positive f64
// inputs built from splitmix64 bits with binary exponents -300 to 300
// (-20 to 60 for fixed) (README.md), reps passes, pass r starting at
// input r. Each result folds len * 1000003 + (exp + 400) * 1009 + the sum of its digits
// but the last into a wrapping u64 sum, trailing zeros dropped: where two
// shortest candidates of one length are equally close (an exact tie),
// Rust's flt2dec breaks it away from even (1.6867057937820423e15, where Go
// and Mk give ...422e15), so the last digit is left out and every column
// agrees (README.md).
// Usage: digits <variant> <reps> <seed>.

import std.math

const N: int = 4096

Mix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

Fold(d: &math.Digits): u64 {
    var s: u64 = 0
    for i in 0..d.len - 1 {
        s = s +% Cast[u64](d.digits[i] - 0x30)
    }
    return Cast[u64](d.len) *% 1000003 +% Cast[u64](d.exp + 400) *% 1009 +% s
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    name := sys.args[1]
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    fixed := name == "fixed"
    xs := try sys.arena.Slice[f64](N)
    for i in 0..N {
        st = st +% 0x9E3779B97F4A7C15
        z := Mix(st)
        // fixed: binary exponents -20 to 60, below ECMA-262's 1e21 for toFixed.
        e := if fixed { 1003 + (z >> 53) % 81 } else { 723 + (z >> 53) % 601 }
        xs[i] = f64.FromBits((e << 52) | (z & 0x000FFFFFFFFFFFFF))
    }
    var acc: u64 = 0
    exact := name == "exact17"
    for r in 0..reps {
        for i in 0..N {
            x := xs[(i + r) & (N - 1)]
            if fixed {
                d := math.ExactDigits(x, 6, math.DigitMode.Fraction)
                acc = acc +% Fold(&d)
            } else if exact {
                d := math.ExactDigits(x, 17, math.DigitMode.Significant)
                acc = acc +% Fold(&d)
            } else {
                d := math.ShortestDigits(x)
                acc = acc +% Fold(&d)
            }
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{acc}\n", (acc))
}
