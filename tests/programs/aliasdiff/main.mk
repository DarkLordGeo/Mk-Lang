// A differential test of the scoped alias facts on slice parameters, kept
// as a regression program. Every call below
// passes slices that are disjoint within the call, so each call keeps the
// loan rule that the alias scopes state; but calls next to each other (loop
// iterations, unrolled copies, tail-recursive steps, the two calls of a
// power iteration) pass overlapping memory in swapped roles. A correct
// compiler prints the same line in dev and release, on both targets
// (aliasdiff.cases). Raw views hold no loans (R19), and no live loan
// covers the memory they write.
import std.raw

Step(b: []int, c: mut []int) {
    c[0] = b[0] +% 1
}

Mix(b: []int, c: mut []int) {
    t := c[0]
    c[0] = b[0] *% 3 +% t
}

Blk(b: []int, c: mut []int) {
    for i in 0..c.Len() {
        c[i] = b[i] +% c[i] *% 2
    }
}

MatVec(a: []f64, x: []f64, y: mut []f64, n: int) {
    for i in 0..n {
        var s := 0.0
        for j in 0..n {
            s += a[i * n + j] * x[j]
        }
        y[i] = s * 0.5
    }
}

// Ping's roles swap at each step: step k reads q[k % 2] and writes the other.
Ping(b: []int, c: mut []int, p: *int, n: int): int {
    c[0] = b[0] *% 5 +% 1
    if n == 0 {
        return c[0]
    }
    unsafe "probe: the next step's views are disjoint from each other" {
        k := n % 2
        return Ping(raw.Slice(raw.Offset(p, 1 - k), 1), raw.SliceMut(raw.Offset(p, k), 1), p, n - 1)
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    a := sys.arena
    var out := sys.Stdout()
    z := sys.args.Len() - 1 // 0 at run time, opaque to the optimizer
    m := 64 + z

    // K1: a pointer table pair, bs[k] and cs[k] = bs[k + 1], so BasicAA
    // cannot relate copy k's store to copy k + 1's load; the loop has a
    // constant trip count of 8, so it unrolls fully after Step inlines.
    xs := try a.Slice[int](m)
    bs := try a.Slice[uint](16)
    cs := try a.Slice[uint](16)
    unsafe "probe: tables of addresses inside xs" {
        p := raw.DataMut(xs)
        for k in 0..16 {
            bs[k] = raw.Addr(raw.Offset(p, 3 * k + z))
            cs[k] = raw.Addr(raw.Offset(p, 3 * (k + 1) + z))
        }
        raw.Write(p, 7)
        for k in 0..8 {
            Step(raw.Slice(raw.FromAddr[int](bs[k]), 1), raw.SliceMut(raw.FromAddr[int](cs[k]), 1))
        }
        for k in 0..8 {
            Mix(raw.Slice(raw.FromAddr[int](bs[k]), 1), raw.SliceMut(raw.FromAddr[int](cs[k]), 1))
        }
    }
    var h1 := 0
    for i in 0..m {
        h1 = h1 *% 31 +% xs[i]
    }

    // K2: the same with a run-time trip count.
    ys := try a.Slice[int](m)
    unsafe "probe: tables of addresses inside ys" {
        p := raw.DataMut(ys)
        for k in 0..16 {
            bs[k] = raw.Addr(raw.Offset(p, 3 * k + z))
            cs[k] = raw.Addr(raw.Offset(p, 3 * (k + 1) + z))
        }
        raw.Write(p, 7)
        for k in 0..15 + z {
            Mix(raw.Slice(raw.FromAddr[int](bs[k]), 1), raw.SliceMut(raw.FromAddr[int](cs[k]), 1))
        }
    }
    var h2 := 0
    for i in 0..m {
        h2 = h2 *% 31 +% ys[i]
    }

    // K3: windows of 4 that slide by 2: call k reads [2k, 2k + 4) and
    // writes [2k + 4, 2k + 8), which call k + 1 and k + 2 then read.
    ws := try a.Slice[int](m)
    for i in 0..m {
        ws[i] = i
    }
    unsafe "probe: sliding windows inside ws" {
        p := raw.DataMut(ws)
        for k in 0..20 + z {
            Blk(raw.Slice(raw.Offset(p, 2 * k), 4), raw.SliceMut(raw.Offset(p, 2 * k + 4), 4))
        }
    }
    var h3 := 0
    for i in 0..m {
        h3 = h3 *% 31 +% ws[i]
    }

    // K4: tail recursion whose views swap roles at every step.
    qs := try a.Slice[int](2)
    qs[0] = 1
    var h4 := 0
    unsafe "probe: two one-element views of qs, swapped per step" {
        p := raw.DataMut(qs)
        h4 = Ping(raw.Slice(p, 1), raw.SliceMut(raw.Offset(p, 1), 1), p, 40 + z)
    }
    q0 := qs[0]
    q1 := qs[1]

    // K5: safe code. A power iteration whose two calls swap x and y, and a
    // loop over SplitAt halves where call k writes what call k + 1 reads.
    n := 16 + z
    am := try a.Slice[f64](n * n)
    xv := try a.Slice[f64](n)
    yv := try a.Slice[f64](n)
    for i in 0..n * n {
        am[i] = Cast[f64](i % 7) * 0.25 - 0.5
    }
    for i in 0..n {
        xv[i] = 1.0
    }
    for _ in 0..10 {
        MatVec(am[..], xv[..], mut yv[..], n)
        MatVec(am[..], yv[..], mut xv[..], n)
    }
    var h5 := 0.0
    for i in 0..n {
        h5 = h5 * 1.5 + xv[i]
    }
    vs := try a.Slice[int](m)
    vs[0] = 3
    for k in 0..m - 1 {
        (left, right) := vs.SplitAt(k + 1)
        Mix(left[k..], mut right[0..1])
    }
    var h6 := 0
    for i in 0..m {
        h6 = h6 *% 31 +% vs[i]
    }
    try Print(mut out, "{h1} {h2} {h3} {h4} {q0} {q1} {h5} {h6}\n", (h1, h2, h3, h4, q0, q1, h5, h6))
}
