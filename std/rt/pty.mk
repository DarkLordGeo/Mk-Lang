// Why: std.proc's Pty runs a child under a pseudo-terminal, which Linux and
// Windows provide in very different ways, and std.sim must be able to
// answer it too.
//
// What: PtyOpen, PtyResize, PtyRead and PtyClose (std-internal), the Windows
// Console record with its reference counting, and PtyUseBegin and PtyUseEnd.
//
// How: the calls go through the Io's call entry as proc.mk's calls do (inline: PtyOpen, PtyResize; pool, whole: PtyClose).
// On Linux the pty is /dev/ptmx's master, one handle, and IoPty.term is
// that descriptor. On Windows it is a pseudo-console (HPCON) over two
// anonymous pipes, and IoPty.term is the address of a Console record that
// holds the HPCON and counts the children Spawn started on it: once every
// such child has been waited for (ChildWait), the console is closed on a
// thread of its own, so its output pipe ends after the last frame and
// PtyRead answers the end there as Linux's EIO does. The close
// runs off the caller's thread because ClosePseudoConsole may wait until
// the output pipe has room for that frame, which only a reader makes. A
// later Spawn on the Pty opens a new pseudo-console of the Pty's size and
// puts its pipes behind the Pty's two runtime handles, as a Linux
// terminal takes a new child after the last one closed it; output of the
// earlier children not yet read goes with the old pipe. A Pty is Share,
// so a Read or WriteAll in another task may be using the old pipe's OS
// handle on a pool thread when a Spawn swaps it: each such call takes its
// OS handle from PtyUseBegin and hands it back to PtyUseEnd, both under
// the Console's lock, and a swap closes an old handle only when no call
// holds it, else the last call to hand it back closes it.

import std.core
import std.raw
import std.sys

// Console is a Windows pseudo-console's state (IoPty.term points at it).
// lock guards every field but refs, and the os field of the read and
// write records: hpc is used only while closed is false, by a holder of
// lock or by a spawn counted in busy; read and write are the Pty's runtime
// handles (KindPty, their dir pointing here), whose OS handles a reopen
// replaces; ruses and wuses count the calls holding read's and write's
// current OS handle (PtyUseBegin), and retired lists the replaced OS
// handles some call still holds; cols and rows the size the last PtyOpen
// or PtyResize asked for. live counts the children Spawn started on it
// and ChildWait has not answered for (a detached or unwaited child stays
// counted, so the console outlives it until PtyClose); waited is set by
// the first such answer. refs counts the Pty and each process record
// whose dir points here; the last release frees it.
struct Console {
    lock: raw.Atomic[u32]
    hpc: uint
    read: uint
    write: uint
    ruses: int
    wuses: int
    retired: *Retired
    cols: int
    rows: int
    live: int
    busy: int
    waited: bool
    closed: bool
    refs: raw.Atomic[i64]
}

// Retired is an OS handle a reopen replaced while uses calls held it; the
// last of them closes it and frees the node (PtyUseEnd).
struct Retired {
    os: uint
    uses: int
    next: *Retired
}

// CloserStack is the closing thread's stack reservation (committed as
// used): ClosePseudoConsole runs in kernel32 on it.
const CloserStack: int = 1 << 20

// ConsoleOf is the Console a Windows IoPty.term names.
ConsoleOf(term: uint): *Console {
    unsafe "term is the address PtyOpen gave a live Console" {
        return raw.FromAddr[Console](term)
    }
}

// ConsoleRelease drops one reference to c and frees it with the last.
// O(1); frees the record.
ConsoleRelease(c: *Console) {
    unsafe "c is live until its last reference is dropped here" {
        // Edge: the Add orders every use of c by this holder before the
        // free by whichever holder drops the last reference.
        if (*c).refs.Add(-1) == 1 {
            HeapFree(raw.PtrCast[Console, u8](c), SizeOf[Console](), AlignOf[Console]())
        }
    }
}

// ConsoleCloser is the closing thread: ClosePseudoConsole of the HPCON
// its argument holds, then the thread ends.
ConsoleCloser(p: *u8) {
    sys.PtyClose(raw.Addr(p))
}

// CloseOnThread starts the closing thread for hpc, so a close never waits
// behind the blocking pool's queue; false when no
// thread can be had, and the caller closes it on the pool.
CloseOnThread(hpc: uint): bool {
    unsafe "hpc is an open HPCON this call hands to the closing thread" {
        t := sys.ThreadStart(ConsoleCloser, raw.FromAddr[u8](hpc), CloserStack)
        if t.err != core.SysErr.None {
            return false
        }
        // Closing a thread's handle does not end the thread.
        _ = sys.Close(Trunc[uint](t.value))
    }
    return true
}

// ConsoleSettle closes c's console when its last child has been waited
// for and no spawn is using it (the caller holds c's lock): it marks c
// closed and starts the closing thread. When no thread can be had the
// console stays open until PtyClose.
ConsoleSettle(c: *Console) {
    unsafe "the caller holds c's lock" {
        if (*c).closed || !(*c).waited || (*c).live != 0 || (*c).busy != 0 {
            return
        }
        if !CloseOnThread((*c).hpc) {
            return
        }
        (*c).closed = true
    }
}

// ConsoleSpawnBegin readies c for a spawn: value the HPCON, counted in
// busy. A closed console is opened again first (sys.PtyOpen at c's size),
// its new pipe ends replacing the old ones in the Pty's handle records; a
// reader still in the old output pipe sees its end. The OS's error when
// that open fails. O(1) system calls; allocates nothing.
ConsoleSpawnBegin(c: *Console): sys.OsResult {
    unsafe "c is the live Console of the caller's Pty; its read and write records are the Pty's, live while it is" {
        Lock(&(*c).lock)
        var r := sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
        if (*c).closed {
            var ends := core.IoWords(a: 0, b: 0, c: 0, d: 0)
            r = sys.PtyOpen((*c).cols, (*c).rows, mut ends)
            if r.err == core.SysErr.None {
                (*c).ruses = ConsoleSwap(c, (*c).read, Trunc[uint](ends.a), (*c).ruses)
                (*c).wuses = ConsoleSwap(c, (*c).write, Trunc[uint](ends.b), (*c).wuses)
                (*c).hpc = Trunc[uint](r.value)
                (*c).closed = false
            }
        }
        if r.err == core.SysErr.None {
            (*c).busy += 1
            r = sys.OsResult(value: Trunc[i64]((*c).hpc), err: core.SysErr.None, os: 0)
        }
        Unlock(&(*c).lock)
        return r
    }
}

// ConsoleSwap puts the OS handle os behind the runtime handle h of c,
// whose old OS handle uses calls hold, and answers 0, the count of calls
// holding os (the caller holds c's lock). With no call holding it the old
// handle is closed; else it goes to c's retired list for the last of them
// to close. When the heap has no node for it, the old handle is left open
// for the process's life: never closed under a call that holds it.
// O(1); allocates one Retired from the Heap when uses is not 0.
ConsoleSwap(c: *Console, h: uint, os: uint, uses: int): int {
    unsafe "the caller holds c's lock; h is a live runtime handle of c's Pty" {
        r := Rec(h)
        old := (*r).os
        (*r).os = os
        if uses == 0 {
            _ = sys.Close(old)
            return 0
        }
        p := HeapAlloc(SizeOf[Retired](), AlignOf[Retired]())
        if raw.IsNull(p) {
            return 0
        }
        n := raw.PtrCast[u8, Retired](p)
        raw.Write(n, Retired(os: old, uses: uses, next: (*c).retired))
        (*c).retired = n
        return 0
    }
}

/// PtyUseBegin answers the OS handle behind h, a Windows Pty end
/// (HandleRec.kind KindPty), and counts the caller as holding it until its
/// PtyUseEnd, so a Spawn that reopens the console does not close that
/// handle under the caller's read or write (std.task's pool paths call
/// both around the job). O(1) under the Console's lock; allocates nothing.
export PtyUseBegin(h: uint): uint {
    unsafe "h is a live KindPty runtime handle: its dir is its Pty's live Console" {
        r := Rec(h)
        c := raw.PtrCast[u8, Console]((*r).dir)
        Lock(&(*c).lock)
        os := (*r).os
        if h == (*c).read {
            (*c).ruses += 1
        } else {
            (*c).wuses += 1
        }
        Unlock(&(*c).lock)
        return os
    }
}

/// PtyUseEnd hands back os, which PtyUseBegin answered for h: the call no
/// longer uses it. When a reopen replaced os meanwhile and this was its
/// last holder, os is closed. O(r) under the Console's lock, r the retired
/// handles still held (at most two a reopen, each held only by calls on a
/// pipe whose console has closed); frees a Retired node when it closes.
export PtyUseEnd(h: uint, os: uint) {
    unsafe "h is a live KindPty runtime handle: its dir is its Pty's live Console" {
        r := Rec(h)
        c := raw.PtrCast[u8, Console]((*r).dir)
        var gone := raw.Null[Retired]()
        Lock(&(*c).lock)
        if os == (*r).os {
            // A retired handle is still open, so the OS never gave its
            // value to the current one: equal values are the same handle.
            if h == (*c).read {
                (*c).ruses -= 1
            } else {
                (*c).wuses -= 1
            }
        } else {
            var prev := raw.Null[Retired]()
            var n := (*c).retired
            for !raw.IsNull(n) {
                if (*n).os == os {
                    (*n).uses -= 1
                    if (*n).uses == 0 {
                        if raw.IsNull(prev) {
                            (*c).retired = (*n).next
                        } else {
                            (*prev).next = (*n).next
                        }
                        gone = n
                    }
                    break
                }
                prev = n
                n = (*n).next
            }
        }
        Unlock(&(*c).lock)
        if !raw.IsNull(gone) {
            _ = sys.Close((*gone).os)
            HeapFree(raw.PtrCast[Retired, u8](gone), SizeOf[Retired](), AlignOf[Retired]())
        }
    }
}

// ConsoleSpawnEnd ends a spawn ConsoleSpawnBegin readied: with started,
// the child counts in live and its process record holds a reference.
ConsoleSpawnEnd(c: *Console, started: bool) {
    unsafe "c is the live Console of the caller's Pty" {
        if started {
            // Edge: the child record's reference is taken while the caller's
            // Pty still holds one (refs >= 1, so no release frees c), and
            // before Spawn returns the handle, so the release of it (ChildWait,
            // or the record's close) comes after this Add.
            _ = (*c).refs.Add(1)
        }
        Lock(&(*c).lock)
        (*c).busy -= 1
        if started {
            (*c).live += 1
        }
        ConsoleSettle(c)
        Unlock(&(*c).lock)
    }
}

// ConsoleChildEnded records that ChildWait answered for a child started on
// c, which may close the console, and drops the child's reference.
ConsoleChildEnded(c: *Console) {
    unsafe "c is live: the child's process record holds a reference" {
        Lock(&(*c).lock)
        (*c).live -= 1
        (*c).waited = true
        ConsoleSettle(c)
        Unlock(&(*c).lock)
    }
    ConsoleRelease(c)
}

PtyOpenJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        w := (*a).w
        var ends := core.IoWords(a: 0, b: 0, c: 0, d: 0)
        r := sys.PtyOpen(Trunc[int](w.a), Trunc[int](w.b), mut ends)
        (*a).w = ends
        Set(a, r)
    }
}

/// PtyOpen makes a pseudo-terminal of cols by rows: p.read and
/// p.write the runtime handles of its controlling side (one handle on
/// Linux), read and written on the blocking pool; p.term what Spawn's
/// IoSpawnReq.pty takes. A size outside 1 to 32,767 is Other; Windows before
/// 10 1809 is Unsupported. Under a simulated Io the handles and term are
/// the simulation's (std.sim). Inline; O(1); allocates one or two
/// HandleRec records from the Heap, and on Windows one Console (Other, with
/// everything closed, when the Heap is exhausted).
export PtyOpen(io: Io, cols: int, rows: int, p: mut core.IoPty): core.IoResult {
    var a := CallOf(core.IoOpPtyOpen, core.IoRouteInline, 0, PtyOpenJob)
    a.w.a = i64(cols)
    a.w.b = i64(rows)
    r := Run(io, mut a)
    if r.err != core.SysErr.None {
        return r
    }
    if !core.IsNative(io) {
        // A simulated Io answers with its own runtime handles and term
        // (std.sim's pty).
        p.read = Trunc[uint](a.w.a)
        p.write = Trunc[uint](a.w.b)
        p.term = Trunc[uint](r.value)
        return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    }
    var term := Trunc[uint](r.value)
    ro := Trunc[uint](a.w.a)
    wo := Trunc[uint](a.w.b)
    hr := NewHandle(ro, false)
    var hw := hr
    if wo != ro {
        hw = NewHandle(wo, false)
    }
    var con := raw.Null[u8]()
    if Target.os == Os.Windows && hr != 0 && hw != 0 {
        con = HeapAlloc(SizeOf[Console](), AlignOf[Console]())
    }
    if hr == 0 || hw == 0 || Target.os == Os.Windows && raw.IsNull(con) {
        if hr != 0 {
            _ = CloseRuntimeHandle(hr)
        } else {
            _ = sys.Close(ro)
        }
        if wo != ro {
            if hw != 0 {
                _ = CloseRuntimeHandle(hw)
            } else {
                _ = sys.Close(wo)
            }
        }
        sys.PtyClose(term)
        return Fail(core.SysErr.Other)
    }
    if Target.os == Os.Windows {
        unsafe "con is a fresh block of Console's size" {
            raw.Write(raw.PtrCast[u8, Console](con), Console(lock: raw.Atomic[u32].New(0), hpc: term, read: hr,
                write: hw, ruses: 0, wuses: 0, retired: raw.Null[Retired](), cols: cols, rows: rows, live: 0,
                busy: 0, waited: false, closed: false, refs: raw.Atomic[i64].New(1)))
            // The ends' reads and writes take their OS handles from the
            // Console (PtyUseBegin), which a reopen replaces.
            (*Rec(hr)).kind = KindPty
            (*Rec(hr)).dir = con
            (*Rec(hw)).kind = KindPty
            (*Rec(hw)).dir = con
        }
        term = raw.Addr(con)
    }
    p.read = hr
    p.write = hw
    p.term = term
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

PtyResizeJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        w := (*a).w
        Set(a, sys.PtyResize(Trunc[uint](w.c), Trunc[int](w.a), Trunc[int](w.b)))
    }
}

/// PtyResize sets the pseudo-terminal p's size; its child sees SIGWINCH on
/// Linux and a resize on Windows, where a console closed after its last
/// child was waited for keeps the size for the next one. A size outside 1
/// to 32,767 is Other. Inline; O(1); allocates nothing.
export PtyResize(io: Io, p: &core.IoPty, cols: int, rows: int): core.IoResult {
    var a := CallOf(core.IoOpPtyResize, core.IoRouteInline, 0, PtyResizeJob)
    a.w.a = i64(cols)
    a.w.b = i64(rows)
    if Target.os != Os.Windows || !core.IsNative(io) {
        a.w.c = Trunc[i64](p.term)
        return Run(io, mut a)
    }
    c := ConsoleOf(p.term)
    unsafe "c is the live Console of p; the inline call holds the lock only for the resize" {
        Lock(&(*c).lock)
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        if cols < 1 || cols > 32767 || rows < 1 || rows > 32767 {
            r = Fail(core.SysErr.Other)
        } else if !(*c).closed {
            a.w.c = Trunc[i64]((*c).hpc)
            r = Run(io, mut a)
        }
        if r.err == core.SysErr.None {
            (*c).cols = cols
            (*c).rows = rows
        }
        Unlock(&(*c).lock)
        return r
    }
}

/// PtyRead reads what the pseudo-terminal p's children wrote, waiting on
/// the blocking pool (a cancel ends the wait with Canceled and nothing
/// read): value the bytes read, 0 at the end, when every child has closed
/// its side (Linux's EIO) or, on Windows, once every child Spawn started
/// on it has been waited for and its last output read (the console closes
/// then and its pipe breaks, 109). O(1) besides the read.
export PtyRead(io: Io, p: &core.IoPty, buf: mut []u8): core.IoResult {
    r := core.IoRead(io, p.read, buf, -1)
    end: i64 = if Target.os == Os.Windows { 109 } else { 5 }
    if r.err != core.SysErr.None && r.err != core.SysErr.Canceled && r.os == end {
        return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    }
    return r
}

PtyCloseJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        sys.PtyClose(Trunc[uint]((*a).w.a))
        Set(a, sys.OsResult(value: 0, err: core.SysErr.None, os: 0))
    }
}

/// PtyClose closes the pseudo-terminal p: its ends first, then (Windows)
/// the pseudo-console, on a closing thread of its own (on the pool, whole,
/// when no thread can be had), unless it closed after its last child was
/// waited for; closing it ends the console's children, which this call
/// does not wait for, so it never waits behind the pool's queue. The first
/// error of the closes is returned; every handle is released whatever it
/// says. O(1); frees the records (a Console once no process record holds
/// it).
export PtyClose(io: Io, p: core.IoPty): core.IoResult {
    // A simulated Io's pty (std.sim) has no console: its two
    // handles are all of it.
    sim := IsForeign(p.read)
    var r := core.IoClose(io, p.read)
    if p.write != p.read {
        w := core.IoClose(io, p.write)
        if r.err == core.SysErr.None {
            r = w
        }
    }
    if Target.os != Os.Windows || sim {
        return r
    }
    c := ConsoleOf(p.term)
    var hpc: uint = 0
    unsafe "c is the live Console of p" {
        Lock(&(*c).lock)
        if !(*c).closed {
            (*c).closed = true
            hpc = (*c).hpc
        }
        // The Pty was moved into this call, so no read or write holds a
        // retired handle any more: none is left unless a call ended
        // without its PtyUseEnd, and those are closed here.
        var n := (*c).retired
        (*c).retired = raw.Null[Retired]()
        Unlock(&(*c).lock)
        for !raw.IsNull(n) {
            next := (*n).next
            _ = sys.Close((*n).os)
            HeapFree(raw.PtrCast[Retired, u8](n), SizeOf[Retired](), AlignOf[Retired]())
            n = next
        }
    }
    if hpc != 0 && !CloseOnThread(hpc) {
        var a := CallOf(core.IoOpPtyClose, core.IoRouteWhole, 0, PtyCloseJob)
        a.w.a = Trunc[i64](hpc)
        _ = Run(core.IoMasked(io), mut a)
    }
    ConsoleRelease(c)
    return r
}
