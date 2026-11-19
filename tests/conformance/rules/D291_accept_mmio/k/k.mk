import std.mmio
import std.raw

export Ack(base: uint): u32 {
    unsafe "base is the local APIC page, mapped uncached" {
        p := raw.FromAddr[u32](base + 0xB0)
        mmio.Store(p, 0)
        mmio.Fence()
        version := mmio.Reg[u32].New(base + 0x30)
        wide := mmio.Reg[uint].New(base + 0x20)
        wide.Write(wide.Read())
        return (version.Read() & 0xFF) | mmio.Load(p)
    }
}

export Swap(p: *uint, q: **u8) {
    unsafe "p and q point at device registers the caller mapped" {
        mmio.Store(p, mmio.Load(p) + 1)
        mmio.Store(q, mmio.Load(q))
    }
}
