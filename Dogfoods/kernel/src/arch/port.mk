// Why: port I/O and privileged instructions have no Mk syntax of their own.
//
// What: Out8, In8, interrupt control, Breakpoint, InvalidOpcode,
// ReadUnmapped, FaultAddress, Exit and Halt.
//
// How: each is one asm statement in an unsafe block whose reason says what
// it touches. Exit writes QEMU's debug-exit port.

import std.mmio
import std.raw

/// The isa-debug-exit device's port: QEMU ends with status (code << 1) | 1
/// for a byte written there.
const DebugExit: u16 = 0xF4

/// Writes `v` to the I/O port `port`. One `out`; touches no memory.
export Out8(port: u16, v: u8) {
    unsafe "out writes one byte to an I/O port and touches no memory" {
        asm("out dx, al", in("dx") port, in("al") v, options(nomem, nostack, preserves_flags))
    }
}

/// Reads one byte from the I/O port `port`. One `in`; touches no memory.
export In8(port: u16): u8 {
    var v: u8 = 0
    unsafe "in reads one byte from an I/O port and touches no memory" {
        asm("in al, dx", out("al") v, in("dx") port, options(nomem, nostack, preserves_flags))
    }
    return v
}

/// Clears the interrupt flag: no interrupt is taken until EnableInterrupts
/// or SleepUntilInterrupt sets it again.
export DisableInterrupts() {
    unsafe "cli changes only the interrupt flag; the asm is not nomem, so no memory access moves across it" {
        asm("cli", options(nostack))
    }
}

/// Sets the interrupt flag. A handler may run at any instruction after
/// this, and may write any memory, so the asm is not `nomem`.
export EnableInterrupts() {
    unsafe "sti changes only the interrupt flag; handlers save every register they use" {
        asm("sti", options(nostack))
    }
}

/// Sets the interrupt flag and halts until an interrupt arrives; returns
/// after its handler, with interrupts enabled. Call it with interrupts
/// disabled right after finding nothing to do: sti's one-instruction shadow
/// means an interrupt that became pending after that check still wakes the
/// hlt instead of being taken before it.
export SleepUntilInterrupt() {
    unsafe "sti then hlt: the handler that wakes the hlt may write any memory, so this asm is not nomem" {
        asm("sti\nhlt", options(nostack))
    }
}

/// Raises vector 3 once; the breakpoint stub returns here with every
/// register intact.
export Breakpoint() {
    unsafe "int3 enters the breakpoint stub, which saves and restores every caller-saved register" {
        asm("int3")
    }
}

/// Executes `ud2`, which raises the invalid-opcode fault; its handler
/// panics, so this never returns.
export InvalidOpcode(): never {
    unsafe "ud2 always faults, and the fault handler panics" {
        asm("ud2", options(noreturn, nostack))
    }
}

/// Reads the first word past the kernel's map with one volatile load, which
/// raises a page fault; its handler panics, so this never returns a value
/// in practice.
export ReadUnmapped(): u64 {
    unsafe "MappedEnd is the first address no page table entry maps, so the load faults and the fault handler panics" {
        return mmio.Load(raw.FromAddr[u64](uint(MappedEnd)))
    }
}

/// Reads CR2, the address of the last page fault.
export FaultAddress(): u64 {
    var v: u64 = 0
    unsafe "mov from cr2 reads a control register and touches no memory" {
        asm("mov {r}, cr2", r: out(reg) v, options(nomem, nostack, preserves_flags))
    }
    return v
}

/// Ends QEMU with status (code << 1) | 1 through isa-debug-exit, and halts
/// for good with interrupts off when the device is absent.
export Exit(code: u8): never {
    Out8(DebugExit, code)
    Halt()
}

/// Stops the CPU for good: interrupts off, then hlt in a loop, since a
/// non-maskable interrupt can still wake it.
export Halt(): never {
    unsafe "cli and hlt touch no memory, and the loop never leaves" {
        asm("cli\n2:\nhlt\njmp 2b", options(noreturn, nostack))
    }
}
