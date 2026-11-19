// verdict: reject E0505
// case: H39
Read2(x: &i32) {
}

Helper(s: &Scope) {
    n: i32 = 1
    s.go Read2(&n) // <- E0505
}
