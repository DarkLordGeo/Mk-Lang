// verdict: reject E0701
// note: raw.AtomicCopy and AtomicFill take a plain T of 1, 2, 4 or 8 bytes aligned to its size, f64 included (memory model R19; [2]u16 is 4 bytes at alignment 2), and raw.FnFromAddr a fn type; pkg.mk lists k in unsafe_modules
import k

Run() {
    k.Use()
}
