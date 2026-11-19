// verdict: reject E0601
// rule: R9
// note: the types decide that a clause is needed, even though the body uses one candidate
Head(a: []i32, b: []i32): &i32 { // <- E0601
    return &a[0]
}
