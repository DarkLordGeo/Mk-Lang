// verdict: reject E0505
// note: the record literal's field q holds the loan on y, so keep cannot take q past y's scope
Caller() {
    x: i32 = 1
    var keep := &x
    if Cond() {
        y: i32 = 2
        (q, p) := (q: &y, p: &x)
        Use(p)
        keep = q // <- E0505
    }
    Use(keep)
}
