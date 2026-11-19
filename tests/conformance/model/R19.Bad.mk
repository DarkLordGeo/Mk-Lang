// verdict: reject E1103
// case: R19.Bad
Peek(p: *i32): i32 {
    return *p // <- E1103
}
