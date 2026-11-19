// verdict: ok
// rule: D-718
// note: `open` is contextual: outside a declaration's head it is a plain name, so a local, a parameter and a field may take it
struct Door {
    open: bool
}

Toggle(open: bool): Door {
    return Door(open: !open)
}

Count(d: Door): int {
    var open := 0
    if d.open {
        open = 1
    }
    return open
}
