// verdict: ok
// rule: R21
// note: the onerr is skipped on exits where r has moved
error Bad

Step(): Bad!void {
}

Hand(): Bad!void {
    r := Acquire("a")
    onerr r.Close()
    try Step()
    Take(r)
}
