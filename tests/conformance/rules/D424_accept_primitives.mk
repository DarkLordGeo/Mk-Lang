// verdict: ok
// rule: D-424
// note: Swap and Replace on any place; SplitAt, GetPair, Chunks and ChunksMut on a slice, ChunksMut under a scope as the parallel for; float bit casts
SwapAndReplace(a: &Alloc): int {
    var x := 1
    var y := 2
    Swap(mut x, mut y)
    var t := HeapText.New(a)
    old := Replace(mut t, HeapText.New(a))
    old.Free(a)
    n := t.Len()
    t.Free(a)
    return x * 10 + y + n
}

Halves(xs: mut []int): int {
    (left, right) := xs.SplitAt(2)
    left[0] = right[0]
    if p := xs.GetPair(0, 1) {
        *p.a += *p.b
    }
    var s := 0
    for c in xs.Chunks(2) {
        s += c.Len()
    }
    for c in xs.ChunksMut(2) {
        c[0] = 0
    }
    return s
}

Scale(xs: mut []f64, k: f64) {
    for x in mut xs {
        *x *= k
    }
}

ScaleAll(io: Io, xs: mut []f64, k: f64) {
    scope s := io.Scope() {
        for piece in xs.ChunksMut(4096) {
            s.go Scale(piece, k)
        }
    }
}

Bits(x: f64, y: f32): bool {
    return f64.FromBits(x.Bits()) == x && f32.FromBits(y.Bits()) == y
}
