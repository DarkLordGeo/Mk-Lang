// verdict: reject E0701
// rule: D-421
// note: an error whose payload field's type has no Format has none, so it does not meet a Format bound
struct Raw {
    n: int
}

error Broken(r: Raw)

Show[T: Format](w: mut Writer, x: &T): WriteError!void {
    try x.Format(w)
}

Report(w: mut Writer): WriteError!void {
    e := Broken(r: Raw(n: 1))
    try Show(w, &e) // <- E0701
}
