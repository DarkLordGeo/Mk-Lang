// The QEMU kernel's main line:
// each step prints one line on COM1; a failed check prints FAIL and
// exits with FailCode, a panic exits with PanicCode, and success prints
// "mk kernel ok" and exits with OkCode. QEMU's status is (code << 1) | 1.

import std.free
import std.simd

const OkCode: u8 = 0x10
const FailCode: u8 = 0x11
const PanicCode: u8 = 0x12

// The Cpuid block of doc/reference.md.
Cpuid(leaf: u32): (a: u32, b: u32, c: u32, d: u32) {
    var a := leaf
    var b: u32 = 0
    var c: u32 = 0
    var d: u32 = 0
    unsafe "cpuid writes only eax to edx, and rbx is kept in t around it" {
        asm("mov {t:r}, rbx\ncpuid\nxchg {t:r}, rbx", t: out(reg) b, inout("eax") a, inout("ecx") c, out("edx") d, options(nomem, nostack, preserves_flags))
    }
    return (a: a, b: b, c: c, d: d)
}

PrintWord(w: u32) {
    PutByte(Trunc[u8](w))
    PutByte(Trunc[u8](w >> 8))
    PutByte(Trunc[u8](w >> 16))
    PutByte(Trunc[u8](w >> 24))
}

Fail(what: str): never {
    Print("FAIL ")
    Print(what)
    Print("\n")
    Exit(FailCode)
}

KernelMain() {
    SerialInit()
    Print("mk kernel: long mode\n")

    id := Cpuid(0)
    Print("cpuid vendor ")
    PrintWord(id.b)
    PrintWord(id.d)
    PrintWord(id.c)
    Print(" max leaf ")
    PrintHex(u64(id.a))
    Print("\n")
    features := simd.Cpu()
    if !features.Has("sse2") {
        Fail("simd.Cpu reports no sse2")
    }
    Print("cpu sse2 yes\n")

    end := ImageEnd()
    Print("kernel end ")
    PrintHex(end)
    Print("\n")
    if end <= 0x100000 || end & 0xFFF != 0 {
        Fail("KernelEnd")
    }

    MapApic()
    version := ApicVersion()
    Print("apic version ")
    PrintHex(u64(version))
    Print("\n")
    if version == 0 {
        Fail("apic version")
    }

    InstallIdt()
    Breakpoint()
    hits := Hits.Load()
    Print("int3 hits ")
    PrintDec(Cast[u64](hits))
    Print("\n")
    if hits != 1 {
        Fail("int3")
    }

    var r := 0
    unsafe "simd.Cpu reported sse2 above, and Entry64 enabled SSE in CR0 and CR4" {
        r = Scaled(17)
    }
    Print("sse2 scaled ")
    PrintDec(Cast[u64](r))
    Print("\n")
    if r != 42 {
        Fail("sse2")
    }

    cs := CSum("mk kernel")
    Print("c sum ")
    PrintDec(cs)
    Print("\n")
    if cs != 889 {
        Fail("c sum")
    }

    sum := HeapSum(1000)
    Print("heap sum ")
    PrintDec(sum)
    Print("\n")
    if sum != 499500 {
        Fail("heap")
    }

    Print("mk kernel ok\n")
    Exit(OkCode)
}

// A panic prints its message and code and exits with PanicCode.
export extern MkPanicHook(info: &free.PanicInfo): never {
    Print("panic ")
    Print(info.message)
    Print(" code ")
    PrintDec(u64(info.code))
    Print("\n")
    Exit(PanicCode)
}
