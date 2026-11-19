// verdict: ok
// case: X.TempUnused
// note: R14: no live holder. The model never used `r`, which is E0208 here; `r` is overwritten before any read, so the temporary's loan is dead when the statement ends
Good() {
    p := MakePoint()
    var r := &MakePoint()
    r = &p
    Use(r)
}
