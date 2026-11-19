// verdict: reject E0408
// case: R1.Dup
Dup() {
    var n := 0
    arr := [mut n; 2] // <- E0408
    Use(&arr)
}
