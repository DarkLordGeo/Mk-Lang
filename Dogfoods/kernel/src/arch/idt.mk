// Why: the kernel takes timer, breakpoint and serial interrupts, and turns
// every CPU fault into a panic with a report.
//
// What: InstallIdt, SetGate, the handlers OnTimer, OnBreakpoint and OnFault,
// Frame, Fault and FaultReport.
//
// How: 64-bit gates of 16 bytes (Intel SDM vol. 3, 6.14). Every stub is
// naked: an interrupt stub saves the caller-saved registers, calls a plain
// Mk handler and returns with iretq; a fault stub pushes the vector (and a
// zero where the CPU pushes no error code) and jumps to FaultEntry, whose
// handler panics.

import std.atomic
import std.raw

/// The vector the local APIC timer raises.
export const TimerVector: u8 = 0x40

/// The vector the local APIC raises for a spurious interrupt.
export const SpuriousVector: u8 = 0xFF

const KernelCode: u64 = 0x08
const InterruptGate: u64 = 0x8E

/// The words a fault leaves on the stack: two pushed by the stub, then the
/// five the CPU pushes.
struct Fault {
    vector: u64
    code: u64
    rip: u64
    cs: u64
    rflags: u64
    rsp: u64
    ss: u64
}

/// The frame the CPU pushes for an interrupt without an error code.
struct Frame {
    rip: u64
    cs: u64
    rflags: u64
    rsp: u64
    ss: u64
}

/// What the fault handler saw, for the panic hook to print.
export struct FaultReport {
    export vector: u64
    export code: u64
    export rip: u64
    export address: u64
}

// 256 gates of two words each. Written only through raw pointers, at boot,
// before lidt names it.
align(16) struct Idt {
    gates: [512]u64
}

packed struct IdtPointer {
    limit: u16
    base: u64
}

var Table: Idt = Idt(gates: [0; 512])
var Ticks: atomic.AtomicInt = atomic.AtomicInt.New(0)
var Breaks: atomic.AtomicInt = atomic.AtomicInt.New(0)
var BreakRip: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var Faulted: atomic.AtomicBool = atomic.AtomicBool.New(false)
var FaultVector: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var FaultCode: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var FaultRip: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var FaultCr2: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

/// Timer interrupts taken since boot.
export TimerTicks(): i64 {
    return Ticks.Load()
}

/// Waits until `n` more timer interrupts have been taken, sleeping between
/// them. The timer must be running.
export WaitTicks(n: i64) {
    target := Ticks.Load() + n
    for {
        DisableInterrupts()
        if Ticks.Load() >= target {
            EnableInterrupts()
            return
        }
        SleepUntilInterrupt()
    }
}

/// Breakpoints taken since boot.
export BreakpointHits(): i64 {
    return Breaks.Load()
}

/// The fault that started the current panic, if a CPU fault started it.
export LastFault(): ?FaultReport {
    if !Faulted.Load() {
        return none
    }
    return some(FaultReport(vector: FaultVector.Load(atomic.Acquire), code: FaultCode.Load(atomic.Acquire), rip: FaultRip.Load(atomic.Acquire), address: FaultCr2.Load(atomic.Acquire)))
}

OnTimer() {
    _ = Ticks.Add(1)
    EndOfInterrupt()
}

OnBreakpoint(frame: *Frame) {
    unsafe "the stub passes the frame the CPU pushed, live for this call" {
        BreakRip.Store((*frame).rip, atomic.Relaxed)
    }
    _ = Breaks.Add(1)
}

// Records the fault for the panic hook, then panics: a fault in this
// kernel is always a bug or a deliberate crash, never something to resume.
OnFault(f: *Fault): never {
    var vector: u64 = 0
    unsafe "FaultEntry passes the words the stub and the CPU pushed, live for this call" {
        vector = (*f).vector
        FaultCode.Store((*f).code, atomic.Relaxed)
        FaultRip.Store((*f).rip, atomic.Relaxed)
    }
    FaultVector.Store(vector, atomic.Relaxed)
    FaultCr2.Store(if vector == 14 { FaultAddress() } else { 0 }, atomic.Relaxed)
    atomic.Fence(atomic.Release)
    Faulted.Store(true)
    match vector {
        0 { Panic("cpu fault: divide error") }
        6 { Panic("cpu fault: invalid opcode") }
        13 { Panic("cpu fault: general protection") }
        14 { Panic("cpu fault: page fault") }
        _ { Panic("cpu fault") }
    }
}

naked TimerStub() {
    unsafe "an interrupt gate enters here; every caller-saved register is saved and the stack is 16-aligned at the call" {
        asm("""
            push rax
            push rcx
            push rdx
            push rsi
            push rdi
            push r8
            push r9
            push r10
            push r11
            cld
            call {h}
            pop r11
            pop r10
            pop r9
            pop r8
            pop rdi
            pop rsi
            pop rdx
            pop rcx
            pop rax
            iretq
            """, h: sym OnTimer)
    }
}

naked SerialStub() {
    unsafe "an interrupt gate enters here; every caller-saved register is saved and the stack is 16-aligned at the call" {
        asm("""
            push rax
            push rcx
            push rdx
            push rsi
            push rdi
            push r8
            push r9
            push r10
            push r11
            cld
            call {h}
            pop r11
            pop r10
            pop r9
            pop r8
            pop rdi
            pop rsi
            pop rdx
            pop rcx
            pop rax
            iretq
            """, h: sym OnSerial)
    }
}

naked BreakpointStub() {
    unsafe "an interrupt gate enters here; every caller-saved register is saved and the stack is 16-aligned at the call" {
        asm("""
            push rax
            push rcx
            push rdx
            push rsi
            push rdi
            push r8
            push r9
            push r10
            push r11
            lea rdi, [rsp + 72]
            cld
            call {h}
            pop r11
            pop r10
            pop r9
            pop r8
            pop rdi
            pop rsi
            pop rdx
            pop rcx
            pop rax
            iretq
            """, h: sym OnBreakpoint)
    }
}

// A spurious interrupt gets no end-of-interrupt (SDM 10.9).
naked SpuriousStub() {
    unsafe "a spurious interrupt needs no work: return at once" {
        asm("iretq")
    }
}

naked DivideStub() {
    unsafe "vector 0 pushes no error code, so the stub pushes a zero, then the vector" {
        asm("push 0\npush 0\njmp {f}", f: sym FaultEntry)
    }
}

naked InvalidOpcodeStub() {
    unsafe "vector 6 pushes no error code, so the stub pushes a zero, then the vector" {
        asm("push 0\npush 6\njmp {f}", f: sym FaultEntry)
    }
}

naked ProtectionStub() {
    unsafe "vector 13 pushes an error code, so the stub pushes only the vector" {
        asm("push 13\njmp {f}", f: sym FaultEntry)
    }
}

naked PageFaultStub() {
    unsafe "vector 14 pushes an error code, so the stub pushes only the vector" {
        asm("push 14\njmp {f}", f: sym FaultEntry)
    }
}

naked FaultEntry() {
    unsafe "every fault stub jumps here with the Fault words on the stack; the handler panics and never returns" {
        asm("""
            mov rdi, rsp
            and rsp, -16
            cld
            call {h}
            ud2
            """, h: sym OnFault)
    }
}

/// The addresses of the stubs, in one asm so the IDT is filled from one
/// read of each.
StubAddresses(): (timer: u64, serial: u64, breakpoint: u64, spurious: u64, divide: u64, invalid: u64, protection: u64, page: u64) {
    var t: u64 = 0
    var r: u64 = 0
    var b: u64 = 0
    var s: u64 = 0
    var d: u64 = 0
    var i: u64 = 0
    var p: u64 = 0
    var f: u64 = 0
    unsafe "lea computes each stub's address and touches no memory" {
        asm("""
            lea {t}, [rip + {ts}]
            lea {r}, [rip + {rs}]
            lea {b}, [rip + {bs}]
            lea {s}, [rip + {ss}]
            lea {d}, [rip + {ds}]
            lea {i}, [rip + {os}]
            lea {p}, [rip + {ps}]
            lea {f}, [rip + {fs}]
            """, t: out(reg) t, r: out(reg) r, b: out(reg) b, s: out(reg) s, d: out(reg) d, i: out(reg) i, p: out(reg) p, f: out(reg) f, ts: sym TimerStub, rs: sym SerialStub, bs: sym BreakpointStub, ss: sym SpuriousStub, ds: sym DivideStub, os: sym InvalidOpcodeStub, ps: sym ProtectionStub, fs: sym PageFaultStub, options(pure, nomem, nostack, preserves_flags))
    }
    return (timer: t, serial: r, breakpoint: b, spurious: s, divide: d, invalid: i, protection: p, page: f)
}

SetGate(base: *u64, vector: u8, stub: u64) {
    lo := (stub & 0xFFFF) | (KernelCode << 16) | (InterruptGate << 40) | (((stub >> 16) & 0xFFFF) << 48)
    hi := stub >> 32
    i := 2 * int(vector)
    unsafe "base is the IDT's first word, the IDT has 512 words, and nothing borrows it at boot" {
        raw.Write(raw.Offset(base, i), lo)
        raw.Write(raw.Offset(base, i + 1), hi)
    }
}

/// Fills the IDT (every vector from 32 up spurious unless named, the four
/// faults the kernel reports, the breakpoint, the timer and COM1) and loads
/// it.
export InstallIdt() {
    stubs := StubAddresses()
    var base: *u64 = raw.Null[u64]()
    unsafe "an Idt is exactly its 512 words" {
        base = raw.PtrCast[Idt, u64](raw.Of(&Table))
    }
    for v in 32..256 {
        SetGate(base, Trunc[u8](v), stubs.spurious)
    }
    SetGate(base, 0, stubs.divide)
    SetGate(base, 3, stubs.breakpoint)
    SetGate(base, 6, stubs.invalid)
    SetGate(base, 13, stubs.protection)
    SetGate(base, 14, stubs.page)
    SetGate(base, TimerVector, stubs.timer)
    SetGate(base, SerialVector, stubs.serial)
    pointer := IdtPointer(limit: 4095, base: u64(raw.Addr(base)))
    unsafe "lidt reads the 10-byte pointer and nothing else" {
        asm("lidt [{p}]", p: in(reg) raw.Of(&pointer), options(readonly, nostack, preserves_flags))
    }
}
