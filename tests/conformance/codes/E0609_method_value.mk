// verdict: reject E0609
// needs: types
// note: a method named without a call is E0609: `c.Get` would capture its receiver, and a function literal that captures is refused; reported by the checker in the types group
struct Counter {
    n: int
}

Counter.Get(&self): int {
    return self.n
}

Apply(c: &Counter, f: fn(&Counter): int): int {
    return f(c)
}

Use(c: &Counter): int {
    return Apply(c, c.Get) // <- E0609
}
