// verdict: ok
// rule: R19
// note: pkg.mk permits unsafe in ffi only; the root module calls it
import ffi

Read(p: *i32): i32 {
    return ffi.Load(p)
}
