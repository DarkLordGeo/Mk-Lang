// verdict: ok
// rule: D-291
// note: std.mmio's Load, Store, Fence and Reg in unsafe, for an integer, a uint and a raw pointer, in a module pkg.mk lists
import k

Run(): u32 {
    return k.Ack(0xFEE00000)
}
