export Stamp(): u64 {
    var lo: u32
    var hi: u32
    unsafe "rdtsc writes only eax and edx" {
        asm("rdtsc", out("eax") lo, out("edx") hi, options(nomem, nostack, preserves_flags))
    }
    return (u64(hi) << 32) | u64(lo)
}

export Twice(x: u64): u64 {
    var y := x
    unsafe "add reads and writes one register" {
        asm("add {y}, {y}", y: inout(reg) y, options(pure, nomem, nostack))
    }
    return y
}
