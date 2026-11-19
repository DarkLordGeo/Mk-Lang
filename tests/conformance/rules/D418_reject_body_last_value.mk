// verdict: reject E0209 E0805
// rule: D-418
// note: a body's last line is not its value: an if there is a statement whose arm values are dropped, and the body can reach its end (the fix writes return)
Pick(c: bool): int {
    if c {
        1 // <- E0209
    } else {
        2 // <- E0209
    }
} // <- E0805
