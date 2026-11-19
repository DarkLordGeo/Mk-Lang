// verdict: ok
// rule: D-291
// note: a write to an extern var goes through its address in unsafe
import k

Run() {
    k.Set(7)
}
