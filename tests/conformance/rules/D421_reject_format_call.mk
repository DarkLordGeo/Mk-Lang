// verdict: reject E0702
// rule: D-421
// note: an error with a field lacking Format has no Format, so a .Format call on it has no impl
struct Raw {
    n: int
}

error Broken(r: Raw)

Show(w: mut Writer): WriteError!void {
    e := Broken(r: Raw(n: 1))
    try e.Format(w) // <- E0702
}
