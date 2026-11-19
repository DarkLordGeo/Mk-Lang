// verdict: ok
// rule: EV528
// rule: R18
// note: a function may declare `ends p` when it ends the whole `p` on every path
Clear(a: mut Arena) ends a {
    a.Reset()
}
