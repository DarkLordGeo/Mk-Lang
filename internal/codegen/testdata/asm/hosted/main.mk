// Inline assembly and naked functions on a hosted x86_64 target, run by
// internal/codegen TestCpuidNative at dev and release.
import std.simd

// The reference's Cpuid block.
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

const Shift: int = 2

// A const operand pasted as text, two inouts, a label and a bare local.
AddShift(x: u64, y: u64): u64 {
    var r := x
    var s := y
    unsafe "adds s shifted left by Shift + 1" {
        asm("shl {y}, {k}\nadd {r}, {y}", inout(reg) r, y: inout(reg) s, k: const Shift + 1, options(pure, nomem, nostack))
    }
    return r
}

// A generic const parameter as a const operand (4.6.1): pasted per instance.
ShlN[const N: int](x: u64): u64 {
    var r := x
    unsafe "shifts r left by the constant N" {
        asm("shl {r}, {n}", inout(reg) r, n: const N, options(pure, nomem, nostack))
    }
    return r
}

// A const value parameter as a const operand, negative, in an i32.
AddK(x: u64, const k: i32): u64 {
    var r := x
    unsafe "adds the constant k" {
        asm("add {r}, {k}", inout(reg) r, k: const k, options(pure, nomem, nostack))
    }
    return r
}

// AT&T syntax, a 32-bit modifier and a lateout.
SubAtt(x: u32, y: u32): u32 {
    var r: u32 = 0
    unsafe "r = x - y" {
        asm("movl {x:e}, {r:e}\nsubl {y:e}, {r:e}", r: out(reg) r, x: in(reg) x, y: in(reg) y, options(att, pure, nomem, nostack))
    }
    return r
}

// xmm registers: a float and a vector.
AddF(x: f64, y: f64): f64 {
    var r := x
    unsafe "r += y in xmm" {
        asm("addsd {r}, {y}", inout(xmm_reg) r, y: in(xmm_reg) y, options(pure, nomem, nostack))
    }
    return r
}

AddVec(a: simd.Vec[i32, 4], b: simd.Vec[i32, 4]): simd.Vec[i32, 4] {
    var r := a
    unsafe "lane-wise add in xmm" {
        asm("paddd {r}, {b}", inout(xmm_reg) r, b: in(xmm_reg) b, options(pure, nomem, nostack))
    }
    return r
}

Seven(): u64 {
    return 7
}

// clobber_abi("C") around a call of a Mk function named by sym; the
// shadow space is reserved by hand (win64 needs it, sysv64 ignores it).
CallSeven(): u64 {
    var r: u64 = 0
    unsafe "Seven follows the C ABI; every caller-saved register is clobbered" {
        asm("sub rsp, 32\ncall {f}\nadd rsp, 32", f: sym Seven, out("rax") r, clobber_abi("C"))
    }
    return r
}

naked JumpSeven(): u64 {
    unsafe "a tail jump to Seven, which takes and returns as JumpSeven does" {
        asm("jmp {s}", s: sym Seven)
    }
}

naked FortyTwo(): u64 {
    unsafe "returns 42 in rax" {
        asm("mov eax, {k}\nret", k: const 40 + 2)
    }
}

export Main(sys: &System): WriteError!void {
    var w := sys.Stdout()
    v := Cpuid(0)
    try Print(mut w, "{b:x} {d:x} {c:x}\n", (b: v.b, d: v.d, c: v.c))
    s := AddVec(simd.Vec[i32, 4].Iota(), simd.Vec[i32, 4].Splat(10)).ReduceSumWrap()
    try Print(mut w, "{a} {b} {c} {s}\n", (a: AddShift(5, 3), b: SubAtt(50, 8), c: AddF(1.5, 2.25), s: s))
    var j: u64 = 0
    var f: u64 = 0
    var c: u64 = 0
    unsafe "naked functions follow the C ABI" {
        j = JumpSeven()
        f = FortyTwo()
    }
    c = CallSeven()
    try Print(mut w, "{j} {f} {c}\n", (j: j, f: f, c: c))
    try Print(mut w, "{p} {q} {r}\n", (p: ShlN[3](5), q: ShlN[1](5), r: AddK(50, -8)))
}