// verdict: ok
// note: a precision witness: the copies of c06, c07 and c03 with the clause the store into the allocator asks for, `*xs into a`, written. Each element is a shallow value (?str, [2]str, str), so it lands in out shallow and no later push walks through it into xs: without shallow loans each push also needs `*xs into xs`. Witness of the precision row M-F3-skip-off; its control, p-clause-ctl.mk, keeps its E0604
// before FLD-3: reject E0604 at 7 E0604, 15 E0604, 24 E0604 (measured by M9 track P2 on m9-item2-r 5f07967, the corpus test harness, 2026-11-15)
Copy2(a: &Arena, xs: &List[?str]): OutOfMemory!List[?str] from a, xs *xs into a {
    var out := List[?str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i])
    }
    return out
}

CopyR(a: &Arena, xs: &List[[2]str]): OutOfMemory!List[[2]str] from a, xs *xs into a {
    var out := List[[2]str].New(a)
    for i in 0..xs.Len() {
        try out.Push(xs[i])
    }
    return out
}

CopyAll(a: &Arena, xs: &List[str]): OutOfMemory!List[str] from a, xs *xs into a {
    var out := List[str].New(a)
    for i in 0..xs.Len() {
        s := xs[i]
        try out.Push(s)
    }
    return out
}

export Main(sys: &System) {
}
