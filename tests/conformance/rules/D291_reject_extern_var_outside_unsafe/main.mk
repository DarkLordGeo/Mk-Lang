// verdict: reject E1101
// rule: D-291
// note: reading an extern var and taking its address need unsafe, even in a module pkg.mk lists
import k

Run(): u8 {
    _ = k.End()
    return k.First()
}
