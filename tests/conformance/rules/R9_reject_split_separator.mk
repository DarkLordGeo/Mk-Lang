// verdict: reject E0501
// rule: R9
// rule: D-204
// note: `str.Split` returns `Split from self, sep`, so `parts` holds the loan on `t` that `t.Str()` made for the separator; `t.Clear()` borrows `t` mut while `parts` is still used
Pieces(line: str, a: &Arena): OutOfMemory!void {
    var t := Text.New(a)
    try t.Push(",")
    var parts := line.Split(t.Str())
    t.Clear() // <- E0501
    for p := parts.Next() {
        Use(&p)
    }
}
