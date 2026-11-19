// verdict: reject E0201
// note: `Shared` belongs to `std.sync` and `AtomicInt` to `std.atomic`, not to the prelude, so each is reached only through its import as `sync.Shared` and `atomic.AtomicInt`
Keep(s: &Shared[i32]): int { // <- E0201
    return 0
}

Count(a: &AtomicInt): int { // <- E0201
    return 0
}
