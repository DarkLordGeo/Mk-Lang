// verdict: parse
// case: R19.Unsafe
// note: parse only: a single-file unit permits no module, so the unsafe block would be E1102
Peek(p: *i32): i32 {
    unsafe "p comes from a live buffer owned by the caller" {
        return *p
    }
}
