// verdict: ok
// rule: D-418
// note: `_ =` is a value position, so the handler's last line is the else's value, which `_ =` then discards (only `_ =` of a void E!void is dropped)
error Bad

Load(i: int): Bad!int {
    if i == 0 {
        fail Bad
    }
    return i
}

Run(w: ?int) {
    _ = w else { 1 }
    _ = Load(0) else { 0 }
    _ = Load(0) else e {
        _ = e
        0
    }
}
