// verdict: reject E1101
// rule: D-291
// note: every std.mmio call needs unsafe, Load, Store, Fence and Reg alike
import std.mmio

Status(p: *u32): u32 {
    return mmio.Load(p) // <- E1101
}

Clear(p: *u32) {
    mmio.Store(p, 0) // <- E1101
}

Order() {
    mmio.Fence() // <- E1101
}

Version(): u32 {
    _ = mmio.Reg[u32].New(0xFEE00030) // <- E1101
    return 0
}
