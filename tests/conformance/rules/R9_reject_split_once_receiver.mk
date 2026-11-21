// verdict: reject E0501
// rule: R9
// note: SplitOnce's result keeps the receiver borrowed through its last use
Pieces(t: mut Text) {
    parts := t.Str().SplitOnce("=").Must()
    t.Clear() // <- E0501
    Use(&parts.after)
}
