// verdict: reject E0701
interface Weighed {
    Weight(&self): int
}

struct Rock {
    w: int
}

Measure[T: Weighed](x: &T): int {
    return x.Weight()
}

Bad(r: &Rock): int {
    return Measure(r) // <- E0701
}
