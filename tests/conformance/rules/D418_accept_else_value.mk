// verdict: ok
// rule: D-418
// note: the block of e else { } and e else x { } yields its last line as the value of e when it is none or an error
Port(text: str): i64 {
    n := text.ParseInt() else e {
        _ = e
        8080
    }
    return n
}

Width(w: ?int): int {
    x := w else { 80 }
    return x
}
