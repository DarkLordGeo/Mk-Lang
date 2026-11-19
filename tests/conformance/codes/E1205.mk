// verdict: reject E1205
// note: one diagnostic for the line, facts.habits [E1205, E1211], target "Add(a: int, b: int): int {"
fn add(a: int, b: int) -> int { // <- E1205
    return a + b
}
