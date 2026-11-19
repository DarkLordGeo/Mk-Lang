// verdict: reject E0706
// rule: D-386
// note: the callee's clause fails for N == 2 of the caller's two values, so the call is refused in the generic
struct Row[const N: int] {
    base: int
}

Row[N].At(self, const i: int): int requires i >= 0 && i < N {
    return self.base + i
}

Fourth[const N: int](r: Row[N]): int requires N == 2 || N == 4 {
    return r.At(3) // <- E0706
}
