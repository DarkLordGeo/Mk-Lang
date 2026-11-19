// verdict: ok
// rule: D-376
// note: a generic function's const fn parameter takes a named function, and a container orders by an interface bound
Twice(x: i64): i64 {
    return x * 2
}

Apply(x: i64, const f: fn(i64): i64): i64 {
    return f(x)
}

struct Sorted[T: Plain + Ord] {
    items: List[T]
}

Sorted[T].Least(&self): ?T {
    if self.items.Len() == 0 {
        return none
    }
    var m := self.items[0]
    for x in self.items {
        if x.Less(&m) {
            m = x
        }
    }
    return some(m)
}

Run(): i64 {
    return Apply(21, Twice)
}
