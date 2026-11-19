// verdict: reject E0402 E0503 E0604
// note: line 1 and the markers are the verdict with shallow loans, line 3 the verdict without them. Copy's out.Push(xs[i]) stores a str read through xs: &HeapList[str]: str is shallow, so the loan of xs's contents lands shallow in out and the second push's walk does not go on through it: `*xs into xs` is not owed. The E0604 that remains asks for `*xs into a`: out holds a loan of a, so the store reaches a. Main still frees ar2 while cp holds its loan through hl (E0503: liveness and conflicts see shallow loans); the two E0402 are Main's early returns before the explicit Free
// before FLD-3: reject E0402 E0503 E0604 at 7 E0604, 16 E0402, 18 E0402, 21 E0503 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy(a: &Arena, xs: &HeapList[str]): OutOfMemory!List[str] from a, xs {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i]) // <- E0604
    }
    return out
}

export Main(sys: &System): WriteError!void {
    var o := sys.Stdout()
    var ar := Arena.New(sys.heap) else { return }
    defer ar.Free()
    var ar2 := Arena.New(sys.heap) else { return } // <- E0402
    s := ar2.Dup("secret-old") else { return }
    var hl := HeapList[str].New(sys.heap.Alloc()) // <- E0402
    hl.Push(sys.heap.Alloc(), s) else { return }
    cp := Copy(&ar, &hl) else { return }
    ar2.Free() // <- E0503
    v := cp[0]
    try Print(mut o, "v={v}\n", (v,))
    hl.FreeEmpty(sys.heap.Alloc())
}
