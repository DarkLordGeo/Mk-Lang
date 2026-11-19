// verdict: reject E0505
// note: field-level loan sets, shape 6: `try ... else` over E!?Pair moves the payload (_t as some).0 whole into a second temporary, field-wise (a payload pair), then takes its fields; v holds the set of field s, the loan on y (M-F8-FR5-payload-key)
struct Pair {
    r: &i32
    s: &i32
}

error Fail

H(a: &i32, b: &i32): Fail!?Pair from (r: a, s: b) {
    return some(Pair(r: a, s: b))
}

Caller(): Fail!void {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r: u, s: v) := try H(&x, &y) else {
            return
        }
        Use(u)
        keep = v // <- E0505
    }
    Use(keep)
}
