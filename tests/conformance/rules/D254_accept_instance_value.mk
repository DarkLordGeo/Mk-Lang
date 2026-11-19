// verdict: ok
// rule: D-254
// note: F[T] and Type[T].M named with every type argument are fn values, stored, passed and called
Same[T](x: T): T {
    return x
}

Pair[T: Plain](a: T, b: T): T {
    return b
}

Apply(f: fn(i64): i64, x: i64): i64 {
    return f(x)
}

struct Box1[T: Plain] {
    v: T
}

Box1[T].Get(&self): T {
    return self.v
}

Run(): i64 {
    f := Same[i64]
    g: fn(i64, i64): i64 = Pair[i64]
    b := Box1[i64](v: 3)
    get := Box1[i64].Get
    return Apply(f, 1) + g(1, 2) + get(&b)
}
