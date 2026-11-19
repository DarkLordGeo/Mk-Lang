// A run test of the scope declarations of scoped alias facts, kept as a
// regression program. Each call passes two
// one-element views that are disjoint, so each call keeps the loan rule
// the alias scopes state, but each loop carries a dependence from one call to
// the next: at distance 1 (xs), 2 (ys) and through floats (fs). The
// wrapping arithmetic leaves the inlined loops without a branch, so the
// loop vectorizer looks at them. If a call's scopes were read across
// iterations, as they are when the scopes are not declared by
// llvm.experimental.noalias.scope.decl in the callee, the vectorizer would
// see no dependence and the printed sums would change. A correct compiler
// prints "1000 500500 250000 2004" in dev and release, on both targets
// (aliaschain.cases). The trip count comes from the argument count, so
// LLVM cannot fold the loops. Raw views hold no loans (R19), and no live
// loan covers the memory they write.
import std.raw

Step(b: []int, c: mut []int) {
    c[0] = b[0] +% 1
}

StepF(b: []f64, c: mut []f64) {
    c[0] = b[0] * 0.5 + 1.0
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    n := 1000 + sys.args.Len() - 1
    xs := try sys.arena.Slice[int](n + 4)
    ys := try sys.arena.Slice[int](n + 4)
    fs := try sys.arena.Slice[f64](n + 4)
    unsafe "each call gets disjoint one-element views; neighbouring calls overlap" {
        p := raw.DataMut(xs)
        for k in 0..n {
            Step(raw.Slice(raw.Offset(p, k), 1), raw.SliceMut(raw.Offset(p, k + 1), 1))
        }
        q := raw.DataMut(ys)
        for k in 0..n {
            Step(raw.Slice(raw.Offset(q, k), 1), raw.SliceMut(raw.Offset(q, k + 2), 1))
        }
        f := raw.DataMut(fs)
        raw.Write(f, 3.0)
        for k in 0..n {
            StepF(raw.Slice(raw.Offset(f, k), 1), raw.SliceMut(raw.Offset(f, k + 1), 1))
        }
    }
    var sx := 0
    var sy := 0
    var sf := 0.0
    for k in 0..n + 1 {
        sx += xs[k]
        sy += ys[k]
        sf += fs[k]
    }
    var out := sys.Stdout()
    a := xs[n]
    try Print(mut out, "{a} {sx} {sy} {sf}\n", (a, sx, sy, sf))
}
