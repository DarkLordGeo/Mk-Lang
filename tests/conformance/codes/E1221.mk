// verdict: reject E1221
// note: a type name from the synonym table, reported by the resolver
Count(xs: []i32): usize { // <- E1221
    return 0
}
