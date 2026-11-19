// verdict: reject E0701
// rule: D-291
// note: mmio's T is one instruction wide, an integer, int, uint or raw pointer; a struct or a float is E0701 even in unsafe
import k

Run() {
    k.Use()
}
