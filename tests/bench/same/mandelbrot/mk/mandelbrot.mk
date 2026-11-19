// mandelbrot, Mk: the reference algorithm of ../README.md. Scalar f64, one
// point at a time, at most 50 iterations, the escape test after each step.
// No FMA (Mk never contracts). The counters' + is checked.
// Usage: mandelbrot <n>. Prints the total iteration count.

const MaxIter: int = 50

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    nf := Cast[f64](n)
    var total := 0
    for y in 0..n {
        ci := (2.0 * Cast[f64](y)) / nf - 1.0
        for x in 0..n {
            cr := (2.0 * Cast[f64](x)) / nf - 1.5
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
            total += i
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
