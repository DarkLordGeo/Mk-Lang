// Why: carriers and pool threads are made by std itself, and the fault
// handler needs each thread's stack bounds and an alternate signal stack to
// report a stack overflow.
//
// What: ThreadStart (ThreadMain), ThreadInit, ThreadYield, StackWords (none
// on macOS), ThreadStackBounds, ThreadMade and the table of threads std
// made.
//
// How: every thread comes from pthread_create, detached, on a stack
// libSystem maps with its own guard page below it; a thread that does not
// come from libSystem cannot hold its per-thread state (errno, the
// pthread record). The bounds come from pthread_get_stackaddr_np and
// pthread_get_stacksize_np. The new thread copies its entry and argument
// out of ThreadStart's frame and then publishes its id there, while
// ThreadStart waits (WaitWord), so the record never outlives the frame.
// Each thread std made records its id in a 512-entry table, which the
// fault handler reads (ThreadMade).

import std.raw

const AltStackBytes: int = 65536
// MaxThreadStack is the largest stack ThreadStart takes (1 TiB), as on Linux.
const MaxThreadStack: int = 1 << 40
// PthreadCreateDetached and PthreadCreateJoinable are Darwin's detach states.
const PthreadCreateJoinable: i32 = 1
const PthreadCreateDetached: i32 = 2
// AttrBytes is pthread_attr_t's size (64 bytes on both architectures).
const AttrBytes: int = 64
const MadeSlots: int = 512

// ThreadRec is the new thread's record, in ThreadStart's frame: the thread
// copies entry and arg, then stores its id in tid and wakes the parent.
struct ThreadRec {
    entry: fn(*u8)
    arg: *u8
    tid: raw.Atomic[u32]
}

// Made holds the ids of the threads std made and that still run (0 free),
// for ThreadMade; each word is only reached atomically (MadeWord).
var Made: [512]u64 = [0; 512]

// MadeWord is entry i of Made as an atomic word.
MadeWord(i: int): *raw.Atomic[u64] {
    unsafe "entry i of the table, an aligned u64 only reached atomically" {
        return raw.PtrCast[u64, raw.Atomic[u64]](raw.Offset(raw.Of(&Made[0]), i))
    }
}

MadeLoad(i: int): u64 {
    unsafe "MadeWord's entry" {
        return (*MadeWord(i)).Load()
    }
}

MadeCas(i: int, old: u64, new: u64): bool {
    unsafe "MadeWord's entry" {
        return (*MadeWord(i)).Cas(old, new)
    }
}

MadeStore(i: int, v: u64) {
    unsafe "MadeWord's entry" {
        (*MadeWord(i)).Store(v)
    }
}

/// ThreadStart starts entry(arg) on a new detached thread with a stack of
/// stack bytes, rounded up to whole pages; value is the thread's pthread_t,
/// the handle ThreadInterrupt takes. stack is 1 to MaxThreadStack bytes;
/// anything else is refused with EINVAL (Other) before anything is made.
/// O(1) system calls; libSystem maps the thread's stack and guard page,
/// kept for the thread's life.
export ThreadStart(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    return Start(entry, arg, stack, false)
}

// Start is ThreadStart and ThreadStartJoin; joinable makes the thread one
// ThreadJoin may join.
Start(entry: fn(*u8), arg: *u8, stack: int, joinable: bool): OsResult {
    if stack <= 0 || stack > MaxThreadStack {
        return Fail(22)
    }
    ps := PageSize()
    size := (stack + ps - 1) / ps * ps
    var rec := ThreadRec(entry: entry, arg: arg, tid: raw.Atomic[u32].New(0))
    var attr: [8]u64 = [0; 8]
    unsafe "attr is a pthread_attr_t of 64 bytes; rec stays in this frame until the thread publishes its id" {
        a := raw.PtrCast[u64, u8](raw.DataMut(mut attr[..]))
        if pthread_attr_init(a) != 0 {
            return Fail(12)
        }
        var rc := pthread_attr_setstacksize(a, Trunc[uint](size))
        if rc == 0 {
            rc = pthread_attr_setdetachstate(a, if joinable { PthreadCreateJoinable } else { PthreadCreateDetached })
        }
        var t: u64 = 0
        if rc == 0 {
            rc = pthread_create(raw.OfMut(mut t), a, ThreadMain, raw.PtrCast[ThreadRec, u8](raw.OfMut(mut rec)))
        }
        _ = pthread_attr_destroy(a)
        if rc != 0 {
            return Fail(i64(rc))
        }
        tw := raw.Of(&rec.tid)
        for (*tw).Load() == 0 {
            WaitWord(tw, 0)
        }
        return Ok(Cast[i64](t))
    }
}

// ThreadMain is every thread's start routine: the alternate signal stack,
// the made table's entry, the handshake, then entry(arg). Returning ends
// the thread through libSystem.
ThreadMain(p: *u8): *u8 {
    unsafe "p is the ThreadRec in ThreadStart's frame, which waits for tid" {
        rec := raw.PtrCast[u8, ThreadRec](p)
        f := (*rec).entry
        arg := (*rec).arg
        _ = ThreadInit()
        e := MadeClaim()
        tw := raw.Of(&(*rec).tid)
        (*tw).Store(1)
        WakeWord(tw)
        f(arg)
        MadeFree(e)
    }
    return raw.Null[u8]()
}

// MadeClaim records the calling thread in the made table; -1 when it is
// full (the thread is then not counted as std's by the fault handler).
MadeClaim(): int {
    id := ThreadId()
    for i in 0..MadeSlots {
        if MadeLoad(i) == 0 && MadeCas(i, 0, id) {
            return i
        }
    }
    return -1
}

MadeFree(e: int) {
    if e >= 0 {
        MadeStore(e, 0)
    }
}

// ThreadMade reports whether the calling thread is the main thread or one
// std made: loads and two pthread calls that take no lock, so a signal
// handler may call it.
ThreadMade(): bool {
    unsafe "pthread_main_np passes no memory" {
        if pthread_main_np() != 0 {
            return true
        }
    }
    id := ThreadId()
    for i in 0..MadeSlots {
        if MadeLoad(i) == id {
            return true
        }
    }
    return false
}

/// ThreadInit gives the thread a 64 KiB alternate signal stack, where the
/// fault handler runs.
/// false when the stack cannot be mapped or installed; it is then unmapped.
/// O(1) system calls; maps the 64 KiB alternate signal stack, kept for the
/// thread's life.
export ThreadInit(): bool {
    return AltStackInit() != 0
}

AltStackInit(): uint {
    p := Reserve(AltStackBytes, 4096)
    if raw.IsNull(p) {
        return 0
    }
    var ss: [3]u64 = [0; 3]
    ss[0] = u64(raw.Addr(p))
    ss[1] = u64(AltStackBytes)
    unsafe "ss is a stack_t: ss_sp, ss_size, ss_flags" {
        if Commit(p, AltStackBytes) && sigaltstack(raw.PtrCast[u64, u8](raw.Data(ss[..])), raw.Null[u8]()) == 0 {
            return raw.Addr(p)
        }
    }
    Release(p, AltStackBytes)
    return 0
}

/// ThreadYield gives the CPU to another ready thread, if there is one.
/// O(1): one system call; allocates nothing.
export ThreadYield() {
    unsafe "sched_yield passes no memory" {
        _ = sched_yield()
    }
}

/// StackWords is the thread's NT_TIB stack words (SysStack.tib); macOS has
/// no NT_TIB, so all four are 0.
/// O(1); allocates nothing.
export StackWords(): [4]u64 {
    return [0; 4]
}

/// ThreadStackBounds is the calling thread's stack [lo, hi) as libSystem
/// mapped it, above its guard page, for a thread std made. (0, 0) on the
/// main thread and on a thread std did not make, as on Linux.
/// O(MadeSlots); allocates nothing.
export ThreadStackBounds(): (lo: uint, hi: uint) {
    unsafe "pthread_main_np passes no memory" {
        if pthread_main_np() != 0 {
            return (lo: 0, hi: 0)
        }
    }
    id := ThreadId()
    var mine := false
    for i in 0..MadeSlots {
        if MadeLoad(i) == id {
            mine = true
        }
    }
    if !mine {
        return (lo: 0, hi: 0)
    }
    unsafe "pthread_self's stack address and size, which libSystem keeps for the thread's life" {
        t := pthread_self()
        hi := raw.Addr(pthread_get_stackaddr_np(t))
        size := pthread_get_stacksize_np(t)
        return (lo: hi - size, hi: hi)
    }
}
