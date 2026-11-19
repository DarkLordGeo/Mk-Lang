// verdict: ok
// rule: R9
// rule: D-204
// note: the twin of R9_reject_split_separator: `t` is changed only after the last use of `parts`, so the separator's loan has ended
Pieces(line: str, a: &Arena): OutOfMemory!void {
    var t := Text.New(a)
    try t.Push(",")
    var parts := line.Split(t.Str())
    for p := parts.Next() {
        Use(&p)
    }
    t.Clear()
}
