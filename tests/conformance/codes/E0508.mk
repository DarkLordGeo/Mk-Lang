// verdict: reject E0508
// prelude: yes
Grow() {
    n := 1
    UseMut(mut n) // <- E0508
}
