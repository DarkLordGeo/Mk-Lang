// Why: a hardware fault (a bad access, a stack overflow) must end the
// process with a report that names the Mk function where it happened, the
// same way on every target.
//
// What: OnFault, the hook std.sys calls with a FaultInfo, and its helpers
// FaultKind, IsGuardPage, FaultSite and GiveFuncTable.
//
// How: std.sys reduces an OS fault to a FaultInfo; OnFault classifies it
// (a stack overflow, a fault in a code region, an illegal instruction or a
// bad access), finds the Mk function containing pc in the function table and
// reports through the panic path from a fixed buffer. It never returns.

import std.raw
import std.sys

// FaultKind refines a fault of kind 2 (every Linux fault from std.sys) or
// 4 (one in a std.vm code region, on both targets): an address in the
// lowest page of a task slot, or in the guard page below a carrier's
// system stack (SetSysStack), is a stack overflow, wherever its pc is.
FaultKind(f: &sys.FaultInfo): u32 {
    if f.kind != 2 && f.kind != 4 {
        return f.kind
    }
    return if IsGuardPage(f.addr) { 1 } else { f.kind }
}

/// IsGuardPage is whether a lies in a guard page std.rt knows: the lowest
/// page of a task slot, or (Linux) the page below a carrier's system stack
/// (SetSysStack). An access there is a stack overflow: OnFault's
/// report and std.runtime's FaultKind.Stack both use it. O(1) for a slot,
/// O(carriers) for the system stacks; allocates nothing.
export IsGuardPage(a: uint): bool {
    top := SlotTop(a)
    if top != 0 {
        base := SlotBase(top)
        if base != 0 && a >= base && a < base + 4096 {
            return true
        }
    }
    return InSysGuard(a)
}

// FaultSite finds the Mk function containing pc in FuncTable,
// within the executable image Init recorded.
FaultSite(pc: uint, sp: uint): *Site {
    // Edge: Init's stores, made before any task or fault handler runs; 0 and 0
    // before Init (the site is then not bounded).
    return SiteIn(FuncTable(), Trunc[uint](Rt.image_lo.Load()), Trunc[uint](Rt.image_hi.Load()), pc, sp)
}

/// GiveFuncTable gives std.sys the function table (sys.FuncRowHook), with
/// which the Windows fault handler tells Mk's code from a C object's.
/// Start calls it once, before FaultHook. O(1); allocates nothing.
export GiveFuncTable() {
    rows := FuncTable()
    unsafe "the rows are FuncTable's, in the image, for the whole process" {
        sys.FuncRowHook(raw.PtrCast[FuncRow, u8](raw.Data(rows)), rows.Len())
    }
}

// SiteIn is FaultSite over rows and the image range [lo, hi): the row with
// the greatest addr <= pc; a __chkstk row means the fault is inside the
// probe, so the caller's return address at [sp + 16] is used. A pc outside
// the image (a system DLL, the heap, a jump to a bad address) gives no
// site, rather than the image's highest function. hi 0 means no
// range is known yet (a fault before Init): pc is then not bounded.
SiteIn(rows: []FuncRow, lo: uint, hi: uint, var pc: uint, sp: uint): *Site {
    var probe := false
    for {
        if hi != 0 && (pc < lo || pc >= hi) {
            return raw.Null[Site]()
        }
        // The first of the rows with the greatest address at or below pc,
        // kept as its fields (kind 255: none yet, past every row kind), in
        // one pass from the last row, so a tie goes to the earlier row.
        var at: uint = 0
        var kind: u32 = 255
        var site := raw.Null[Site]()
        var i := rows.Len()
        for i > 0 {
            i -= 1
            a := raw.Addr(rows[i].addr)
            if a <= pc && (kind == 255 || a >= at) {
                at = a
                kind = rows[i].kind
                site = rows[i].site
            }
        }
        // sp 0: no faulting stack to read (the backtrace), so a probe row
        // gives no site.
        if kind != 1 || probe || sp == 0 {
            return if kind != 0 { raw.Null[Site]() } else { site }
        }
        unsafe "sp is the faulting stack; __chkstk pushed rcx and rax" {
            pc = raw.Read(raw.FromAddr[uint](sp + 16))
        }
        probe = true
    }
}

// Hex16 writes x as 16 lowercase hex digits.
Hex16(buf: mut []u8, at: int, x: uint) {
    for i in 0..16 {
        d := Trunc[u8]((x >> Trunc[uint](60 - 4 * i)) & 15)
        if d < 10 {
            buf[at + i] = 48 + d
        } else {
            buf[at + i] = 87 + d
        }
    }
}

/// OnFault is the hook std.sys calls.
/// Writes the report from a fixed buffer and allocates nothing; the process
/// exits. O(rows) to find the site.
export OnFault(f: &sys.FaultInfo): never {
    kind := FaultKind(f)
    site := FaultSite(f.pc, f.sp)
    var text: [64]u8 = [0; 64]
    if kind == 1 {
        // Edge: as Report's store: a racing panic of another thread may
        // overwrite it; only the winner's hook reads it (hook.mk).
        Panics.code.Store(PanicStackLimit) // the hook's code (hook.mk)
        unsafe "h is the header of the faulting task's slot, or null" {
            h := SlotOf(raw.FromAddr[u8](f.sp))
            if !raw.IsNull(h) && (*h).id > 1 {
                var n := Put(mut text[..], 0, " in task ")
                n = Digits(mut text[..], n, (*h).id)
                Reporting(site, "stack limit", raw.Str(raw.Data(text[..]), n), "")
            }
        }
        Reporting(site, "stack limit", "", "")
    }
    if kind == 4 {
        // A fault in a std.vm code region that no filter resumed: not a
        // fault in Mk code, the same report and exit 101 on every target.
        Reporting(raw.Null[Site](), "fault in a code region", "", "")
    }
    // Kind 3, an illegal instruction, names its pc; an access fault, the
    // address it touched.
    var n := Put(mut text[..], 0, "0x")
    Hex16(mut text[..], n, if kind == 3 { f.pc } else { f.addr })
    n += 16
    unsafe "text holds n bytes of ASCII" {
        Reporting(site, if kind == 3 { "illegal instruction at " } else { "invalid memory access at " }, raw.Str(raw.Data(text[..]), n), "")
    }
}
