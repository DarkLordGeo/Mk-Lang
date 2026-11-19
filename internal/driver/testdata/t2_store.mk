Cond(): bool {
    return true
}

Use(p: &i32) {
    _ = p
}

Hole() {
    long: i32 = 1
    var out := &long
    if Cond() {
        short: i32 = 2
        r := &short
        a := 1
        b := 2
        c := a + b
        _ = c
        d := 3
        _ = d
        e := 4
        _ = e
        out = r
    }
    Use(out)
}
