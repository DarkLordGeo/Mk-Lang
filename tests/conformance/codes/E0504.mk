// verdict: reject E0504
// prelude: yes
Read() {
    var n := 1
    a := mut n
    Use(&n) // <- E0504
    *a += 1
}
