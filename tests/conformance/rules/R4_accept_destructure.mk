// verdict: ok
// rule: R4
struct Job {
    id: int
    res: Res
}

Finish(j: Job): int {
    Job(id, res) := j
    res.Close()
    return id
}
