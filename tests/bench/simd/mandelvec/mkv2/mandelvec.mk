// mandelvec, Mk, SIMD table: the kernel of README.md (same/mandelbrot's
// algorithm, L points at once) with std.simd's Vec and Mask, at the build's
// fixed CPU target and no run-time dispatch. This package is the v2
// variant's, L = 2 (Vec[f64, 2]: one xmm register at x86-64-v2);
// ../mk is the v3 variant's, the same text with L = 4 (one ymm), since
// Mk has no compile-time fact of the build's CPU (Target.vector_bits never
// follows --cpu) and C and Zig choose L from the target at compile time.
// Each lane stops counting at its escape; the loop ends when no lane is
// active or at 50 iterations. Mk never contracts a product into a sum, so
// every count is the scalar one. The lane counts are f64, as in the
// C and Rust cells. Usage: mandelvec <n>. Prints the total iteration count.

import std.simd

const MaxIter: int = 50
const L: int = 2

type V = simd.Vec[f64, L]
type M = simd.Mask[f64, L]

// Point is the scalar loop, for the columns left over at the end of a row.
Point(cr: f64, ci: f64): int {
    var zr := 0.0
    var zi := 0.0
    var i := 0
    for i < MaxIter {
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

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    nf := Cast[f64](n)
    two := V.Splat(2.0)
    four := V.Splat(4.0)
    one := V.Splat(1.0)
    zero := V.Zero()
    var total := 0
    for y in 0..n {
        ci := (2.0 * Cast[f64](y)) / nf - 1.0
        civ := V.Splat(ci)
        var x := 0
        for x + L <= n {
            var crs: [L]f64 = [0.0; L]
            for l in 0..L {
                crs[l] = (2.0 * Cast[f64](x + l)) / nf - 1.5
            }
            cr := V.FromArray(crs)
            var zr := zero
            var zi := zero
            var cnt := zero
            var active := M.FromBits(0xFF)
            for _ in 0..MaxIter {
                tr := (zr * zr - zi * zi) + cr
                ti := (two * zr) * zi + civ
                zr = tr
                zi = ti
                esc := zr * zr + zi * zi > four
                active = active & !esc
                if !active.Any() {
                    break
                }
                cnt = cnt + active.Select(one, zero)
            }
            for c in cnt.ToArray() {
                total += Cast[int](c)
            }
            x += L
        }
        for x < n {
            total += Point((2.0 * Cast[f64](x)) / nf - 1.5, ci)
            x += 1
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
