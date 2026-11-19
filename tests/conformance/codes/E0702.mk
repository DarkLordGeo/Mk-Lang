// verdict: reject E0702
// note: Weight is a method of an interface in scope that Rock has no impl of, so the checker reports E0702 rather than E0203
interface Weighed {
    Weight(&self): int
}

struct Rock {
    w: int
}

Heavy(r: &Rock): int {
    return r.Weight() // <- E0702
}
