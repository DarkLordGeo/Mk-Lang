// verdict: reject E1102
// rule: D-291
// note: section and extern var are allowed only in a module listed in unsafe_modules, and pkg.mk lists none
import k

Run(): u32 {
    return k.Magic()
}
