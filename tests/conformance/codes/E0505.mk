// verdict: reject E0505
// prelude: yes
Escape() {
    long: i32 = 1
    var r := &long
    if Cond() {
        short: i32 = 2
        r = &short // <- E0505
    }
    Use(r)
}
