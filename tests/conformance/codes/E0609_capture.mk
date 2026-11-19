// verdict: reject E0609
// note: a fn literal that uses a parameter of the enclosing body; anchored at the first captured use
Apply(f: fn(i32): i32): i32 {
    return f(1)
}

Shift(k: i32): i32 {
    return Apply(fn(x: i32): i32 { return x + k }) // <- E0609
}
