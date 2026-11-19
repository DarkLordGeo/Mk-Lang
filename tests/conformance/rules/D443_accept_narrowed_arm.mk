// verdict: ok
// rule: D-443
// note: an arm checked under if T is X does not decide the type when another arm does; the narrowed arm is checked against T read as X
Either[T: Plain](v: T, w: int): T {
    x := if T is int { w } else { v }
    return x
}

Optional[T: Plain](v: T): ?T {
    x: ?T = if T is int { none } else { some(v) }
    return x
}

Call(): int {
    _ = Optional(true)
    _ = Either(false, 1)
    return Either(2, 3)
}
