// verdict: reject E0402
// note: no implicit drop at the scope end; the rewrite is `defer f.Close()`
Leak() {
    f := Acquire("f") // <- E0402
    Use(&f)
}
