// verdict: reject E0505
// note: field-level loan sets, `try` on a fallible per-field call: the call's temporary has the success type, so the destructuring takes its fields directly; s holds the loan on y, r only the loan on x (FR5-try-ok.mk keeps r and is accepted)
struct Pair {
    r: &i32
    s: &i32
}

G(a: &i32, b: &i32): OutOfMemory!Pair from (r: a, s: b) {
    return Pair(r: a, s: b)
}

Caller(): OutOfMemory!void {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        Pair(r, s) := try G(&x, &y)
        Use(r)
        keep = s // <- E0505
    }
    Use(keep)
}
