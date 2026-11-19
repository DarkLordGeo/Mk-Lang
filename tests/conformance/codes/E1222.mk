// verdict: reject E1222
Widen(n: i32): i64 {
    return n as i64 // <- E1222
}
