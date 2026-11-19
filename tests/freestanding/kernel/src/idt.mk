// Interrupt entry, as in doc/examples.md "Interrupt entry": an IDT
// whose vector 3 is a naked stub
// that saves the caller-saved registers, calls OnBreakpoint with the frame
// the CPU pushed, restores them and returns with iretq.

import std.atomic
import std.raw

struct Frame {
    rip: u64
    cs: u64
    rflags: u64
    rsp: u64
    ss: u64
}

var Hits: atomic.AtomicInt = atomic.AtomicInt.New(0)
var LastRip: atomic.AtomicInt = atomic.AtomicInt.New(0)

// The IDT: 256 gates of two words each.
align(16) struct Idt {
    words: [512]u64
}

var Table: Idt = Idt(words: [0; 512])

packed struct IdtPtr {
    limit: u16
    base: u64
}

OnBreakpoint(frame: *Frame) {
    unsafe "the stub passes the frame the CPU pushed, live for this call" {
        LastRip.Store(Trunc[i64]((*frame).rip))
    }
    _ = Hits.Add(1)
}

naked Int3Stub() {
    unsafe "an interrupt gate enters here; every caller-saved register is saved, and the stack is aligned at the call" {
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

StubAddress(): u64 {
    var addr: u64 = 0
    unsafe "lea computes the stub's address and touches nothing" {
        asm("lea {r}, [rip + {s}]", r: out(reg) addr, s: sym Int3Stub, options(pure, nomem, nostack, preserves_flags))
    }
    return addr
}

// InstallIdt fills vector 3 with an interrupt gate (present, ring 0, type
// 0xE) to Int3Stub through the code selector 0x08, and loads the IDT.
InstallIdt() {
    stub := StubAddress()
    lo := (stub & 0xFFFF) | (0x08 << 16) | (0x8E << 40) | (((stub >> 16) & 0xFFFF) << 48)
    hi := stub >> 32
    var base: *u64 = raw.Null[u64]()
    unsafe "an Idt is exactly its words, written at boot under no live loan" {
        base = raw.PtrCast[Idt, u64](raw.Of(&Table))
        raw.Write(raw.Offset(base, 6), lo)
        raw.Write(raw.Offset(base, 7), hi)
    }
    ptr := IdtPtr(limit: 4095, base: u64(raw.Addr(base)))
    unsafe "lidt reads the 10-byte pointer and nothing else" {
        // readonly without pure keeps the asm, as Out8's nomem does.
        asm("lidt [{p}]", p: in(reg) raw.Of(&ptr), options(readonly, nostack, preserves_flags))
    }
}

// Breakpoint raises vector 3 once.
Breakpoint() {
    unsafe "int3 enters Int3Stub, which preserves every register and returns here" {
        asm("int3")
    }
}
