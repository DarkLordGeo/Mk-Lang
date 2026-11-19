// verdict: reject E0204
// needs: types
// rule: EV533
// rule: R4
// note: only the module that declares `Pair` may destructure it, because its fields are private
import util

Sum(p: util.Pair): i32 {
    util.Pair(a, b) := p // <- E0204
    return a + b
}
