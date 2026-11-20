// verdict: reject E1221
// needs: types
// note: a member spelled as Rust spells it names the Mk member
First(xs: ?int): int {
    return xs.unwrap() // <- E1221
}
