// verdict: ok
// rule: R18
// rule: S-G135
// note: the twin of EV528_reject_ends_one_path; both paths call `a.Reset()`, which ends the whole parameter, so the wrapper needs no unsafe-permitted module
Clear(a: mut Arena, c: bool, n: mut int) ends a {
    if c {
        *n = 1
        a.Reset()
    } else {
        a.Reset()
    }
}
