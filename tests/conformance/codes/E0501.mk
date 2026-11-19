// verdict: reject E0501
Twice() {
    var n := 1
    a := mut n
    b := mut n // <- E0501
    *a += 1
    *b += 1
}
