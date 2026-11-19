// verdict: reject E0209
// rule: D-418
// note: an if at statement position is a statement, and a non-void last line there is a dropped value
Pick(c: bool) {
    if c {
        1 // <- E0209
    } else {
    }
}
