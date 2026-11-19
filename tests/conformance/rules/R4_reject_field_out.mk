// verdict: reject E0409
// rule: R4
// note: no partial moves, with a reference in the way
struct Job {
    id: int
    res: Res
}

Finish(j: &Job) {
    Take(j.res) // <- E0409
}
