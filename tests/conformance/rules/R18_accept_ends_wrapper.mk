// verdict: ok
// rule: R18
// note: a function may declare ends p when it calls an ends function on p on every path
Clear(a: mut Arena) ends a {
    a.Reset()
}
