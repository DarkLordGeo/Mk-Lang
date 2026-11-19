// Why: the timer and the serial interrupt come through the local APIC, and
// the legacy PICs must not interfere.
//
// What: the APIC register constants and the functions that remap and mask
// the 8259 PICs, enable the APIC and start its periodic timer.
//
// How: memory-mapped registers (Intel SDM vol. 3, chapter 10) through
// std.mmio. MapApic must have mapped the APIC page uncached before any
// function here runs.

import std.mmio
import std.raw

/// The timer count for a 10 ms period: the local APIC timer under QEMU
/// counts a 1 GHz bus clock, which StartTimer divides by 16.
export const TenMilliseconds: u32 = 625000

/// The local APIC's default physical base.
export const ApicBase: u64 = 0xFEE00000

const IdRegister: u64 = 0x20
const VersionRegister: u64 = 0x30
const EoiRegister: u64 = 0xB0
const SpuriousRegister: u64 = 0xF0
const TimerLvt: u64 = 0x320
const Lint0Lvt: u64 = 0x350
const TimerInitial: u64 = 0x380
const TimerCurrent: u64 = 0x390
const TimerDivide: u64 = 0x3E0

const SoftwareEnable: u32 = 0x100
const Periodic: u32 = 0x20000
const Masked: u32 = 0x10000
const DivideBy16: u32 = 0x3
const ExternalInterrupt: u32 = 0x700

Register(offset: u64): mmio.Reg[u32] {
    unsafe "a Reg names the register; MapApic mapped the APIC page uncached, so every access reaches the device" {
        return mmio.Reg[u32].New(uint(ApicBase + offset))
    }
}

ReadRegister(offset: u64): u32 {
    reg := Register(offset)
    unsafe "the register is mapped uncached and reading it has no side effect" {
        return reg.Read()
    }
}

WriteRegister(offset: u64, v: u32) {
    reg := Register(offset)
    unsafe "the register is mapped uncached; the write configures the APIC and nothing else" {
        reg.Write(v)
    }
}

/// The local APIC's id, from bits 24 to 31 of its id register.
export ApicId(): u32 {
    return ReadRegister(IdRegister) >> 24
}

/// The local APIC's version, the low byte of its version register.
export ApicVersion(): u32 {
    return ReadRegister(VersionRegister) & 0xFF
}

/// The timer's current count, read with one volatile load.
export TimerCount(): u32 {
    unsafe "the current-count register is mapped uncached and reading it has no side effect" {
        return mmio.Load(raw.FromAddr[u32](uint(ApicBase + TimerCurrent)))
    }
}

/// Signals the end of the interrupt being handled, with one volatile store
/// of zero to the EOI register.
export EndOfInterrupt() {
    unsafe "a write of zero to the EOI register ends the in-service interrupt and nothing else" {
        mmio.Store(raw.FromAddr[u32](uint(ApicBase + EoiRegister)), 0)
    }
}

/// Remaps the 8259 PICs to vectors 0x20 to 0x2F, so a stray legacy
/// interrupt lands on a spurious gate instead of a fault's, and masks every
/// line but COM1's; then enables the local APIC with its spurious vector
/// and lets the PIC's output in through LINT0 as an external interrupt.
export EnableInterruptController() {
    Out8(0x20, 0x11)
    Out8(0xA0, 0x11)
    Out8(0x21, 0x20)
    Out8(0xA1, 0x28)
    Out8(0x21, 0x04)
    Out8(0xA1, 0x02)
    Out8(0x21, 0x01)
    Out8(0xA1, 0x01)
    Out8(0x21, 0xEF)
    Out8(0xA1, 0xFF)
    WriteRegister(SpuriousRegister, SoftwareEnable | u32(SpuriousVector))
    WriteRegister(Lint0Lvt, ExternalInterrupt)
}

/// Starts the timer in periodic mode at the bus clock divided by 16,
/// raising TimerVector every `count` ticks. The fence orders the IDT's
/// writes before the first interrupt can arrive.
export StartTimer(count: u32) {
    unsafe "a fence touches no memory of its own" {
        mmio.Fence()
    }
    WriteRegister(TimerDivide, DivideBy16)
    WriteRegister(TimerLvt, Periodic | u32(TimerVector))
    WriteRegister(TimerInitial, count)
}

/// Stops the timer: masks its entry and clears its count.
export StopTimer() {
    WriteRegister(TimerLvt, Masked | u32(TimerVector))
    WriteRegister(TimerInitial, 0)
}

/// Signals the master PIC that the legacy interrupt being handled is done.
export EndOfLegacyInterrupt() {
    Out8(0x20, 0x20)
}
