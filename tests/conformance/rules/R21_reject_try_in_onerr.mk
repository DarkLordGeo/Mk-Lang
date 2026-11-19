// verdict: reject E0806
// rule: R21
Step(): Bad!void {
}

error Bad

Clean(): Bad!void {
    r := Acquire("a")
    onerr {
        try Step() // <- E0806
        r.Close()
    }
    try Step()
    r.Close()
}
