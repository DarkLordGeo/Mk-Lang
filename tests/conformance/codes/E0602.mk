// verdict: reject E0602
Second(a: &i32, b: &i32): &i32 from a {
    return b // <- E0602
}
