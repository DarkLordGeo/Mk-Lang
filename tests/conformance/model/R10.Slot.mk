// verdict: reject E0502
// case: R10.Slot
// also: H57
struct Slot {
    m: mut i32
}

Peek(s: &Slot): &i32 {
    return &*s.m
}

Alias() {
    var n: i32 = 1
    var s := Slot(m: mut n)
    a := Peek(&s)
    *s.m = 2 // <- E0502
    Use(a)
}
