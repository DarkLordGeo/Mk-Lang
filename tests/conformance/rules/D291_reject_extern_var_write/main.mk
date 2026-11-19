// verdict: reject E0404
// rule: D-291
// note: an extern var is no Mk var: assigned or borrowed mut it is E0404 even in unsafe
import k

Run() {
    k.Clear()
}
