// verdict: ok
// rule: D-909
// note: `become` replaces the frame: a direct tail call, and one through a table of `fn` values whose type is the caller's signature; the arguments are register-sized, and a reference parameter is forwarded at its own position
const Ops: [2]fn(&[]int, int, int): int = [Step, Skip]

Step(xs: &[]int, i: int, acc: int): int {
    if i >= xs.Len() {
        return acc
    }
    become Ops[xs[i] % 2](xs, i + 1, acc + xs[i])
}

Skip(xs: &[]int, i: int, acc: int): int {
    if i >= xs.Len() {
        return acc
    }
    become Step(xs, i + 1, acc)
}

Count(n: int, acc: int): int {
    if n == 0 {
        return acc
    }
    become Count(n - 1, acc + 1)
}
