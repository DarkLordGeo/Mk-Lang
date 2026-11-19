// verdict: reject E1102
// rule: EV528
// rule: R18
// note: `ends a` is not proven on the path where `c` is false; borrow reports E1102 at the signature in the flow group
Clear(a: mut Arena, c: bool) ends a { // <- E1102
    if c {
        a.Reset()
    }
}
