// The one cpu "sse2" function of a general-regs-only kernel:
// it saves the FPU state with fxsave64, computes in SSE
// registers, restores the state with fxrstor64, and takes and returns no
// float.

import std.raw

align(16) struct FxArea {
    bytes: [512]u8
}

var Fx: FxArea = FxArea(bytes: [0; 512])

// Scaled is n * 2.5 + 0.25, truncated toward zero, computed as f64.
Scaled(n: int): int cpu "sse2" {
    var p: *FxArea = raw.Null[FxArea]()
    unsafe "fxsave64 writes the 512-byte area, which no loan covers" {
        p = raw.Of(&Fx)
        asm("fxsave64 [{p}]", p: in(reg) p, options(nostack, preserves_flags))
    }
    x := Cast[f64](n) * 2.5 + 0.25
    r := Cast[int](x)
    unsafe "fxrstor64 reads back the state fxsave64 wrote" {
        asm("fxrstor64 [{p}]", p: in(reg) p, options(nostack, preserves_flags))
    }
    return r
}
