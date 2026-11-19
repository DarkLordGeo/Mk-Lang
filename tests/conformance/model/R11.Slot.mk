// verdict: reject E0505
// case: R11.Slot
// also: H37
struct Slot {
    r: mut &i32
}

Set(s: mut Slot, v: &i32 into s) {
    *s.r = v
}

Caller() {
    long: i32 = 1
    var p := &long
    if Cond() {
        short: i32 = 2
        var b := Slot(r: mut p)
        Set(mut b, &short) // <- E0505
    }
    Use(p)
}
