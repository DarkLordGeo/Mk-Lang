// verdict: reject E1203
Get(x: &'a i32): i32 { // <- E1203
    return *x
}
