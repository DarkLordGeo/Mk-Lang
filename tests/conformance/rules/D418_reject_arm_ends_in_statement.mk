// verdict: reject E0310
// rule: D-418
// note: a value arm whose last line is a statement has no value: a destructure, _ =, a compound assignment, defer, onerr and scope each end an arm without one
Pair(): (a: int, b: int) {
    return (a: 1, b: 2)
}

Destructure(c: bool): int {
    x := if c {
        1
    } else {
        (a: _, b: _) := Pair() // <- E0310
    }
    return x
}

Discard(c: bool): int {
    x := if c {
        1
    } else {
        _ = 2 // <- E0310
    }
    return x
}

Compound(c: bool): int {
    var k := 0
    x := if c {
        1
    } else {
        k += 1 // <- E0310
    }
    return x + k
}

Deferred(c: bool): int {
    k := 1
    x := if c {
        1
    } else {
        defer Use(&k) // <- E0310
    }
    return x
}

OnErr(c: bool): OutOfMemory!int {
    k := 1
    x := match c {
        true { 1 }
        false {
            onerr Use(&k) // <- E0310
        }
    }
    return x
}

Scoped(c: bool, io: Io): int {
    x := if c {
        1
    } else {
        scope s := io.Scope() { // <- E0310
            _ = s
        }
    }
    return x
}
