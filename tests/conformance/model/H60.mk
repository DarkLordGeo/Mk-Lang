// verdict: reject E0505
// case: H60
Head(xs: mut []i32): &i32 {
    return &xs[0]
}

Caller() {
    long: i32 = 1
    var r := &long
    if Cond() {
        var arr: [4]i32 = [0; 4]
        r = Head(mut arr[..]) // <- E0505
    }
    Use(r)
}
