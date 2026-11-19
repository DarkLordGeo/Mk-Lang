// verdict: reject E1102
// needs: imports
// note: pkg.mk lists only ffi in unsafe_modules, so the root module may not write unsafe
import ffi

Peek(p: *u8): u8 {
    unsafe "the caller passes a valid pointer" { // <- E1102
        return *p
    }
}

Read(p: *u8): u8 {
    return ffi.Load(p)
}
