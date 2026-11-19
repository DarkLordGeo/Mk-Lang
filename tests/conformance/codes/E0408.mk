// verdict: reject E0408
// prelude: yes
Fill(xs: mut []i32) {
    ys := [xs; 2] // <- E0408
    Use(&ys)
}
