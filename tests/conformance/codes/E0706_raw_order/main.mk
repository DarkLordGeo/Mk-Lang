// verdict: reject E0706
// note: a bad order given to one of std.raw's ordered atomics is E0706 from the form's requires clause on its const order, never an internal compiler error (memory model R19); pkg.mk lists k in unsafe_modules
import k

Run(): int {
    return k.Bad()
}
