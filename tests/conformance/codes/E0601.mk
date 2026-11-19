// verdict: reject E0601
Pick(a: &i32, b: &i32, first: bool): &i32 { // <- E0601
    if first {
        return a
    }
    return b
}
