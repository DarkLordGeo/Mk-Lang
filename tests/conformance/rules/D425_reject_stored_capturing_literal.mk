// verdict: reject E0609
// rule: D-425
// note: a literal that reads a local cannot be stored in a field; the one way is a fn value whose first parameter takes the state by reference, stored beside the plain state
struct Filter {
    keep: fn(i32): bool
}

Make(limit: i32): Filter {
    return Filter(keep: fn(x: i32): bool {
        return x > limit // <- E0609
    })
}
