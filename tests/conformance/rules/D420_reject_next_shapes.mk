// verdict: reject E0301
// rule: D-420
// note: only a Next with no other parameter and a ?T result with no error set makes a value iterable: an error set, an extra parameter or a result that is not optional is refused at the source
error Bad

struct Faulty {
    n: int
}

Faulty.Next(mut self): Bad!?int {
    if self.n == 0 {
        fail Bad
    }
    return none
}

struct Extra {
    n: int
}

Extra.Next(mut self, k: int): ?int {
    _ = k
    return none
}

struct Bare {
    n: int
}

Bare.Next(mut self): int {
    return self.n
}

Loop() {
    for x in Faulty(n: 1) { // <- E0301
        _ = x
    }
    for y in Extra(n: 1) { // <- E0301
        _ = y
    }
    for z in Bare(n: 1) { // <- E0301
        _ = z
    }
}
