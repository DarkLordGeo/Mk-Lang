// verdict: ok
// rule: D-386
// note: a callee's clause over the caller's const generic holds for every value the caller's own clause allows, so no restatement is needed
struct Row[const N: int] {
    base: int
}

Row[N].At(self, const i: int): int requires i >= 0 && i < N {
    return self.base + i
}

Second[const N: int](r: Row[N]): int requires N == 2 || N == 4 {
    return r.At(1)
}

Use2(): int {
    return Second(Row[2](base: 10)) + Second(Row[4](base: 20))
}
