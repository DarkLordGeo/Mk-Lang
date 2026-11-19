// verdict: reject E0505
// note: on Eat's error edge h is restored with its field sets and mode, so the onerr's h.b holds y (mutant M-F8-S2-d122-drop: the restore keeps the moved, emptied field sets)
struct Hold {
    a: &i32
    b: &i32
    m: mut i32
}

Eat(h: Hold): OutOfMemory!void {
    Use(h.a)
}

Caller(): OutOfMemory!void {
    x: i32 = 1
    var z: i32 = 0
    var h := Hold(a: &x, b: &x, m: mut z)
    onerr Use(h.b)
    if Cond() {
        y: i32 = 2
        h.b = &y // <- E0505
        try Eat(h)
    }
}
