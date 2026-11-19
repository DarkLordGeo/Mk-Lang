// verdict: ok
// rule: R11
struct Slot {
    r: mut &i32
}

Set(s: mut Slot, v: &i32 into s) {
    *s.r = v
}

Caller() {
    a: i32 = 1
    b: i32 = 2
    var p := &a
    var s := Slot(r: mut p)
    Set(mut s, &b)
    Use(&s)
}
