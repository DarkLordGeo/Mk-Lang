// verdict: reject E1103
Poke(p: *i32) {
    *p = 1 // <- E1103
}
