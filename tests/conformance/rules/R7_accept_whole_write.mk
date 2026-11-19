// verdict: ok
// rule: R7
// note: writing a whole local replaces its held set
struct Ref1 {
    r: &i32
}

Retarget() {
    var n: i32 = 1
    m: i32 = 2
    var h := Ref1(r: &n)
    h = Ref1(r: &m)
    n = 3
    Use(&h)
}
