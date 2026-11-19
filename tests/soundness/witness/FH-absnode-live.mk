// verdict: reject E0501
// note: a stored reborrow kept live through a parameter's node: F stores `&*xs` into its parameter h (`xs into h`), takes e through h, then pushes into xs until the list regrows, and reads the freed *e. The loan is held only by h's node, so the liveness search walks a node's predecessors, and the push is E0501, as with a local h. Accepted, `mk run` prints `through e after the pushes: 0; xs[0] now: 7`; a change of this verdict reopens the hole
struct H {
    r: &HeapList[i32]
}

struct P {
    v: i32
    w: i32
}

First(h: &H): &i32 from h {
    return &h.r.Items()[0]
}

F(h: mut H, xs: mut HeapList[i32] into h, a: &Alloc): OutOfMemory!P {
    h.r = &*xs
    e := First(&*h)
    for i in 0..64 {
        try xs.Push(a, Cast[i32](i) + 1000) // <- E0501
    }
    v := *e
    w := xs.Items()[0]
    return P(v: v, w: w)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    a := sys.heap.Alloc()
    var xs := HeapList[i32].New(a)
    defer xs.Free(a)
    var ys := HeapList[i32].New(a)
    defer ys.Free(a)
    try xs.Push(a, 7)
    var h := H(r: &ys)
    p := try F(mut h, mut xs, a)
    try Print(mut out, "through e after the pushes: {v}; xs[0] now: {w}\n", (v: p.v, w: p.w))
}
