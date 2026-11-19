// verdict: ok
// rule: EV529
// rule: R19
// note: pkg.mk permits `unsafe` in `ffi` only; the `unsafe` block there has its reason string
import ffi

Step(n: i32): i32 {
    return ffi.Next(n)
}
