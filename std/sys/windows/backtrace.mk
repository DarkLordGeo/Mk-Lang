// Why: the panic report prints a backtrace, and on Windows x64 the stack is
// walked by unwind data, not by frame pointers.
//
// What: UnwindsFrames (true), UnwindFrames, and the CONTEXT offsets it
// reads (ContextRbx and the rest).
//
// How: the frames are walked as the vectored handler's FaultOwner walks
// (fault.mk): RtlLookupFunctionEntry and RtlVirtualUnwind from the caller's
// own context, read by inline asm.

import std.raw

// CONTEXT's general registers.
const ContextRbx: int = 0x90
const ContextRbp: int = 0xA0
const ContextRsi: int = 0xA8
const ContextRdi: int = 0xB0
const ContextR12: int = 0xD8

/// UnwindsFrames is whether UnwindFrames walks the stack (Windows) or
/// returns -1 for the caller to follow the frame chain: a constant, so a
/// program links only its target's walk (hello's 64 KiB budget).
export const UnwindsFrames: bool = true

/// UnwindFrames writes the running thread's return addresses into pcs,
/// innermost first, starting at its own frame, and returns how many it
/// wrote: it stops at a frame with no unwind entry (past the first, but
/// for the function a trapped call resumed from, which it steps over as
/// a leaf; mk_trap_call's own frame is not listed), a pc of 0, a stack
/// pointer outside the thread's stack as the TEB records it, or pcs' end.
/// -1 when it cannot walk (Linux: the caller follows the frame chain
/// instead). Its own context is read by inline asm, not
/// RtlCaptureContext, so no import is added to every program: the
/// callee-saved registers this function does not save hold its caller's
/// values all through it, and those it saves the unwind restores from its
/// frame, so reading each at its own point gives the context at rip.
/// O(frames) system calls; allocates nothing (the CONTEXT, 1,248 bytes,
/// is in this frame).
export UnwindFrames(pcs: mut []uint): int {
    var buf: [156]u64 = [0; 156]
    var v_rip: uint = 0
    var v_rsp: uint = 0
    var v_rbx: uint = 0
    var v_rbp: uint = 0
    var v_rsi: uint = 0
    var v_rdi: uint = 0
    var v_r12: uint = 0
    var v_r13: uint = 0
    var v_r14: uint = 0
    var v_r15: uint = 0
    unsafe "reads this function's own registers" {
        asm("lea {x}, [rip]", x: out(reg) v_rip)
        asm("mov {x}, rsp", x: out(reg) v_rsp)
        asm("mov {x}, rbx", x: out(reg) v_rbx)
        asm("mov {x}, rbp", x: out(reg) v_rbp)
        asm("mov {x}, rsi", x: out(reg) v_rsi)
        asm("mov {x}, rdi", x: out(reg) v_rdi)
        asm("mov {x}, r12", x: out(reg) v_r12)
        asm("mov {x}, r13", x: out(reg) v_r13)
        asm("mov {x}, r14", x: out(reg) v_r14)
        asm("mov {x}, r15", x: out(reg) v_r15)
    }
    var n := 0
    unsafe "c is buf's 16-aligned CONTEXT; the walk reads only inside the thread's stack as the TEB records it" {
        c := raw.FromAddr[u8]((raw.Addr(raw.DataMut(mut buf[..])) +% 15) & ~Trunc[uint](15))
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRip)), v_rip)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRsp)), v_rsp)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRbx)), v_rbx)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRbp)), v_rbp)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRsi)), v_rsi)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextRdi)), v_rdi)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextR12)), v_r12)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextR12 + 8)), v_r13)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextR12 + 16)), v_r14)
        raw.Write(raw.PtrCast[u8, uint](raw.Offset(c, ContextR12 + 24)), v_r15)
        rip := raw.PtrCast[u8, uint](raw.Offset(c, ContextRip))
        rsp := raw.PtrCast[u8, uint](raw.Offset(c, ContextRsp))
        teb := Teb()
        lo := raw.Read(raw.PtrCast[u8, uint](raw.Offset(teb, 0x10)))
        room := raw.Read(raw.PtrCast[u8, uint](raw.Offset(teb, 0x08))) -% lo -% 8
        // A trapped call (fault.mk TrapResume) ran its callee from
        // mk_trap_call, whose machine frame returns to the trap: that frame
        // is the call's, not one of the program's, so it is not listed;
        // and the function that trapped may be a frameless leaf, with no
        // entry, whose return address is at its stack pointer.
        trap := raw.Addr(TrapEntry())
        var leaf := false
        for _ in 0..WalkFrames {
            if n >= pcs.Len() {
                break
            }
            at := raw.Read(rip)
            s := raw.Read(rsp)
            if at == 0 || s -% lo >= room {
                break
            }
            var base: uint = 0
            e := RtlLookupFunctionEntry(at, raw.OfMut(mut base), raw.Null[u8]())
            resumed := trap != 0 && !raw.IsNull(e) && base +% uint(raw.Read(raw.PtrCast[u8, u32](e))) == trap
            if !resumed {
                pcs[n] = at
                n += 1
            }
            if raw.IsNull(e) {
                if !leaf {
                    break
                }
                leaf = false
                raw.Write(rip, raw.Read(raw.FromAddr[uint](s)))
                raw.Write(rsp, s +% 8)
                continue
            }
            leaf = resumed
            var data: u64 = 0
            var frame: uint = 0
            _ = RtlVirtualUnwind(0, base, at, e, c, raw.PtrCast[u64, u8](raw.OfMut(mut data)), raw.OfMut(mut frame), raw.Null[u8]())
        }
    }
    return n
}
