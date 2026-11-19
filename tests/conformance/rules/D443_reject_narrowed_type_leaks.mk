// verdict: reject E0301
// rule: D-443
// note: the construct's type is T, decided by the arm that is not narrowed, so it is not an int outside the arms
Either[T: Plain](v: T, w: int): int {
    x := if T is int { w } else { v }
    return x // <- E0301
}
