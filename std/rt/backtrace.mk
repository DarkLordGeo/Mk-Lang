// Why: a panic report that names only the panic site leaves the reader to
// guess how the program got there; the backtrace lists the calls.
//
// What: BacktraceWanted and WriteBacktrace, with FnSite (a Mk function's
// row in Rt.func_table), ChainFrames and StackRange.
//
// How: after the report's first line, one line per frame of the reporting
// thread's stack: a Mk function's name and the file of its definition, from
// Rt.func_table (each row site is an FnSite, internal/codegen functable.go);
// a frame outside Mk code (C, a system DLL, a code region, a support
// routine) prints its address. The frames come from sys.UnwindFrames
// (Windows: RtlVirtualUnwind, as FaultOwner walks), or on Linux from the
// frame chain when the build has frame pointers on; with them off the
// report says so (there is no .eh_frame unwinder).
// The walk stops at a frame it cannot unwind. Frames carry no line numbers
// (the line tables are not read); the site keeps its exact line.
//
// The backtrace is written unless MK_BACKTRACE is 0 or off (in the
// environment the process started with). The report's first line
// is unchanged, and every reader that compares a report (the corpus, the
// driver's panic tests) reads it up to the line "backtrace:".

import std.raw
import std.sys

// FramePointers is the build's --frame-pointers setting (a constant of
// every pack). O(1); allocates nothing.
FramePointers(): bool

// FramePointer reads rbp (x29 on aarch64) by inline asm: under
// --frame-pointers the head of the frame chain. O(1); allocates nothing.
FramePointer(): uint

// FnSite is a Mk function's row site: its Site, then its name's length,
// then the name's bytes, at FnNameAt (codegen's fnSite).
struct FnSite {
    site: Site
    nlen: u32
}

// FnNameAt is the offset of an FnSite's name bytes.
const FnNameAt: int = 28

// BacktraceFrames is the most frames a backtrace walks.
const BacktraceFrames: int = 64

/// BacktraceWanted is whether the report gets its backtrace: yes unless
/// MK_BACKTRACE is 0 or off. O(environment); allocates nothing.
export BacktraceWanted(): bool {
    // Byte compares, not ==: no string compare code joins every program's
    // report (hello's budget).
    if v := Env("MK_BACKTRACE") {
        b := v.AsBytes()
        return !(b.Len() == 1 && b[0] == 48 || b.Len() == 3 && b[0] == 111 && b[1] == 102 && b[2] == 102)
    }
    return true
}

// Under is whether file lies under the directory d ("std/rt/"), by bytes.
Under(file: str, d: str): bool {
    f := file.AsBytes()
    p := d.AsBytes()
    if f.Len() < p.Len() {
        return false
    }
    for i in 0..p.Len() {
        if f[i] != p[i] {
            return false
        }
    }
    return true
}

/// WriteBacktrace writes the running thread's frames to standard error:
/// "backtrace:" then one line per frame, from the first frame outside the
/// report's own code (std.rt and std.sys) on; or "backtrace: frame
/// pointers were off" on a target walked by the frame chain when the build
/// has none. Reads only inside the running stack's bounds. O(frames x
/// rows); allocates nothing (1 KiB of stack).
export WriteBacktrace() {
    var pcs: [64]uint = [0; 64]
    w := Writer()
    // A target with no unwinder of its own needs the frame chain; a
    // constant, so Windows links no chain walk (hello's budget).
    chain := !sys.UnwindsFrames
    if chain && !FramePointers() {
        w("backtrace: frame pointers were off\n".AsBytes())
        return
    }
    // The header first: a fault in the walk (a nested panic at stage 2,
    // which only exits) leaves it and the report's line written.
    w("backtrace:\n".AsBytes())
    var n := if chain { ChainFrames(mut pcs[..], FramePointer()) } else { sys.UnwindFrames(mut pcs[..]) }
    var skipping := true
    for i in 0..n {
        // Every frame is a return address, past its call: the call is at
        // pc - 1, inside the caller even when the call is its last
        // instruction.
        pc := pcs[i]
        row := FaultSite(pc -% 1, 0)
        // The line's pieces: "  ", the name, " (", the file, ")\n"; or
        // "  ", 0x and the address in hex, and "\n" outside Mk code.
        var hex: [18]u8 = [48; 18]
        var parts: [5]str = ["  ", "", " (", "", ")\n"]
        if raw.IsNull(row) {
            if skipping {
                continue
            }
            // The digits from the last, as many as the address needs (a
            // loop of data-dependent length stays a small loop), then 0x.
            var x := pc
            var k := 17
            for {
                d := Trunc[u8](x & 15)
                hex[k] = if d < 10 { 48 + d } else { 87 + d }
                x = x >> 4
                k -= 1
                if x == 0 || k < 2 {
                    break
                }
            }
            hex[k] = 120
            hex[k - 1] = 48
            unsafe "hex[k - 1..18] is ASCII" {
                parts[1] = raw.Str(raw.Offset(raw.Data(hex[..]), k - 1), 19 - k)
            }
            parts[2] = ""
            parts[4] = "\n"
        } else {
            unsafe "row's site is a Mk function's FnSite (codegen's funcRows): its name's bytes follow it, alive for the process" {
                f := raw.PtrCast[Site, FnSite](row)
                parts[1] = raw.Str(raw.Offset(raw.PtrCast[Site, u8](row), FnNameAt), int((*f).nlen))
                parts[3] = (*row).file
                if (*f).nlen == 0 {
                    // std's rows carry no name (hello's budget): the file.
                    parts[2] = ""
                    parts[4] = "\n"
                }
            }
            if skipping {
                if Under(parts[3], "std/rt/") || Under(parts[3], "std/sys/") {
                    continue
                }
                skipping = false
            }
        }
        // Each piece in order, as the report's long form is.
        for p in parts {
            w(p.AsBytes())
        }
    }
}

// BtWrite holds sys.FatalWrite's fn value as a word (Writer).
var BtWrite: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// Writer is sys.FatalWrite, called through a word an atomic load reads,
// so the optimizer cannot inline its console code at each of the
// backtrace's writes: every program carries the backtrace, and hello's
// size budget (64 KiB) has no room for those copies.
Writer(): fn([]u8) {
    var f: fn([]u8) = sys.FatalWrite
    unsafe "f's bytes are a fn value's; the word holds them" {
        // Edge: only this thread reads it back (the panicking one); a
        // racing writer stores the same bytes.
        BtWrite.Store(raw.Read(raw.PtrCast[fn([]u8), u64](raw.Of(&f))))
        var b := BtWrite.Load()
        return raw.Read(raw.PtrCast[u64, fn([]u8)](raw.OfMut(mut b)))
    }
}

// ChainFrames follows the frame chain from fp, System V's layout (the
// saved frame pointer at fp, the return address above it), into pcs while
// each frame lies inside the running stack and above the last; the
// number of frames.
ChainFrames(pcs: mut []uint, var fp: uint): int {
    (lo, hi) := StackRange()
    var n := 0
    unsafe "each read is of 16 bytes inside the running stack's bounds" {
        for n < pcs.Len() {
            if fp < lo || fp > hi -% 16 || fp & 7 != 0 {
                break
            }
            next := raw.Read(raw.FromAddr[uint](fp))
            ra := raw.Read(raw.FromAddr[uint](fp + 8))
            if ra == 0 {
                break
            }
            pcs[n] = ra
            n += 1
            if next <= fp {
                break
            }
            fp = next
        }
    }
    return n
}

// StackRange is the running stack: a task's slot below its header, or the
// thread's own stack; (0, 0) when neither is known.
StackRange(): (lo: uint, hi: uint) {
    slot := CurrentSlot()
    if !raw.IsNull(slot) {
        hi := raw.Addr(slot)
        return (lo: SlotBase(hi + Trunc[uint](HeaderBytes)), hi: hi)
    }
    b := sys.ThreadStackBounds()
    return (lo: b.lo, hi: b.hi)
}
