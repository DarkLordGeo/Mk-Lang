// Why: in system-libc mode glibc keeps per-thread state (errno, malloc's
// per-thread cache, the stack canary at %fs:0x28) in a thread control block,
// which a bare clone would share with its parent; such threads must come
// from glibc.
//
// What: PthreadStart and PthreadMain, the thread path of system-libc mode,
// and PthreadSlack.
//
// How: every thread is made by glibc's pthread_create, detached, on a stack
// this file maps. glibc places its control block and the static TLS at the
// top of the given stack, so the mapping holds PthreadSlack bytes more. The
// thread records its stack in the stack table as ThreadStart's clone path
// does: exactly stack bytes, ending at the page boundary above its first
// frame, with a guard page right below.

import std.raw

// PthreadRec is the new thread's record, in PthreadStart's frame: the
// parent waits on tid (a futex word) for ThreadStart's value, and the
// thread reads the other fields before it publishes tid.
struct PthreadRec {
    entry: fn(*u8)
    arg: *u8
    base: uint
    stack: int
    tid: raw.Atomic[u32]
}

const PthreadDetached: uint = 1

// PthreadSlack is the room above the recorded stack for glibc's thread
// control block, its static TLS and the thread's first frames (glibc
// 2.39 takes a few KiB).
const PthreadSlack: int = 65536

// PthreadStackMin is glibc's PTHREAD_STACK_MIN, the smallest stack
// pthread_attr_setstack takes: 16 KiB on x86_64, 128 KiB on aarch64.
PthreadStackMin(): int {
    return if Target.arch == Arch.Arm64 { 131072 } else { 16384 }
}

// PthreadStart is ThreadStart's system-libc branch (stack already checked):
// a guard page, then the stack, then PthreadSlack (more when the two are
// under PthreadStackMin), all committed but the guard, handed to
// pthread_create detached. value is the new thread's tid.
// O(1) system calls; maps the thread's stack, kept for the thread's life.
PthreadStart(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    size := (stack + 4095) / 4096 * 4096
    var total := 4096 + size + PthreadSlack
    if total - 4096 < PthreadStackMin() {
        // More slack, above the recorded stack, for glibc's floor.
        total = 4096 + PthreadStackMin()
    }
    base := Reserve(total, 4096)
    if raw.IsNull(base) {
        return FailWith(MapError(12))
    }
    unsafe "the mapping is the new thread's stack for its whole life; the table's slots are the libc functions the entry bound" {
        if !Commit(raw.Offset(base, 4096), total - 4096) {
            Release(base, total)
            return FailWith(MapError(12))
        }
        _ = StackTable()
        var rec := PthreadRec(entry: entry, arg: arg, base: raw.Addr(base), stack: size, tid: raw.Atomic[u32].New(0))
        var attr: [8]u64 = [0; 8]
        a := raw.PtrCast[u64, u8](raw.DataMut(mut attr[..]))
        var w := LibcFn(LcAttrInit)
        init := raw.Read(raw.PtrCast[u64, fn(*u8): i32](raw.OfMut(mut w)))
        if init(a) != 0 {
            Release(base, total)
            return Fail(12)
        }
        w = LibcFn(LcAttrSetStack)
        set_stack := raw.Read(raw.PtrCast[u64, fn(*u8, *u8, uint): i32](raw.OfMut(mut w)))
        w = LibcFn(LcAttrSetDetach)
        set_detach := raw.Read(raw.PtrCast[u64, fn(*u8, i32): i32](raw.OfMut(mut w)))
        w = LibcFn(LcAttrDestroy)
        destroy := raw.Read(raw.PtrCast[u64, fn(*u8): i32](raw.OfMut(mut w)))
        var rc := set_stack(a, raw.Offset(base, 4096), Trunc[uint](total - 4096))
        if rc == 0 {
            rc = set_detach(a, i32(PthreadDetached))
        }
        if rc == 0 {
            var id: u64 = 0
            w = LibcFn(LcPthreadCreate)
            create := raw.Read(raw.PtrCast[u64, fn(*u64, *u8, fn(*u8): *u8, *u8): i32](raw.OfMut(mut w)))
            rc = create(raw.OfMut(mut id), a, PthreadMain, raw.PtrCast[PthreadRec, u8](raw.OfMut(mut rec)))
        }
        _ = destroy(a)
        if rc != 0 {
            Release(base, total)
            return Fail(i64(rc))
        }
        // The thread publishes its tid before it runs entry; rec stays in
        // this frame until then.
        tw := raw.Of(&rec.tid)
        for {
            t := (*tw).Load()
            if t != 0 {
                return Ok(i64(t))
            }
            _ = Syscall(Nrs().futex, raw.Addr(tw), FutexWaitPrivate, 0, 0, 0, 0)
        }
    }
}

// PthreadMain is every system-libc thread's start routine. Its stack is
// [lo, hi): hi the page boundary above its own frame (the scheduler's frames
// under it are inside, as on the clone path; above it is glibc's block),
// lo = hi - stack, with the page below lo
// made the guard (the mapping's own guard page when the slack is used up
// exactly). Then the alternate signal stack, the stack table's entry, the
// tid for the parent, and entry(arg). Returning ends the thread through
// glibc; the stack is never unmapped, as on the clone path.
PthreadMain(p: *u8): *u8 {
    var top: u64 = 0
    hi := (raw.Addr(raw.Of(&top)) + 4095) & ~Trunc[uint](4095)
    unsafe "p is the PthreadRec in PthreadStart's frame, which waits for tid" {
        rec := raw.PtrCast[u8, PthreadRec](p)
        f := (*rec).entry
        arg := (*rec).arg
        floor := (*rec).base + 4096
        var lo := hi - Trunc[uint]((*rec).stack)
        if lo < floor {
            lo = floor
        }
        if lo > floor {
            _ = GuardPage(raw.FromAddr[u8](lo - 4096))
        }
        _ = ThreadInit()
        e := StackClaim(lo, hi)
        tw := raw.Of(&(*rec).tid)
        (*tw).Store(Trunc[u32](ThreadId()))
        _ = Syscall(Nrs().futex, raw.Addr(tw), FutexWakePrivate, 1, 0, 0, 0)
        f(arg)
        StackFree(e)
    }
    return raw.Null[u8]()
}
