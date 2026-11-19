// verdict: reject E1101
// rule: D-420
// note: the loop calls Next as it.Next() would, so a Next with a cpu clause the caller lacks is refused outside unsafe
struct Wide {
    n: int
}

Wide.Next(mut self): ?int cpu "avx2" {
    return none
}

Loop() {
    for x in Wide(n: 1) { // <- E1101
        _ = x
    }
}
