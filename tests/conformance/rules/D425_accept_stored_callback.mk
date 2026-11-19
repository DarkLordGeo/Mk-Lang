// verdict: ok
// rule: D-425
// note: callbacks kept for later in a List, each a fn value beside its plain state, called through the field with a reference to the state
struct Bounds {
    lo: i32
    hi: i32
}

struct Check {
    pass: fn(&Bounds, i32): bool
    state: Bounds
}

Inside(b: &Bounds, x: i32): bool {
    return x >= b.lo && x < b.hi
}

Outside(b: &Bounds, x: i32): bool {
    return !Inside(b, x)
}

Passing(a: &Arena, x: i32): OutOfMemory!int {
    var checks := List[Check].New(a)
    try checks.Push(Check(pass: Inside, state: Bounds(lo: 0, hi: 10)))
    try checks.Push(Check(pass: Outside, state: Bounds(lo: 5, hi: 6)))
    var n := 0
    for c in checks.Items() {
        if c.pass(&c.state, x) {
            n += 1
        }
    }
    return n
}
