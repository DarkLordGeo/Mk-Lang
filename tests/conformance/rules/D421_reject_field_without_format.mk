// verdict: reject E0706
// rule: D-421
// note: an error whose payload field's type has no Format has none, and printing it is refused at the hole
struct Raw {
    n: int
}

error Broken(r: Raw)

Report(sys: &System) {
    e := Broken(r: Raw(n: 1))
    sys.PrintErr("{e}\n", (e,)) else {} // <- E0706
}
