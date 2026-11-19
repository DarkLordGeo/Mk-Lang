// Why: GetQueuedCompletionStatusEx's own timeout runs on the system timer
// tick, so a short poll timeout on Windows overshoots by up to a tick.
//
// What: high-resolution poll timeouts for PollWait: the timer slots, how
// they are armed and how they are taken back.
//
// How: measured on a Windows 11 development machine, a 1 ms PollWait with
// nothing to complete lasted 11.0 ms on average (TestRt_PollerWindows,
// waitres, 20 waits, without this file). So a wait with a finite timeout also arms a
// high-resolution waitable timer (CREATE_WAITABLE_TIMER_HIGH_RESOLUTION,
// kernel32) and associates it with the port through a wait completion
// packet (NtAssociateWaitCompletionPacket, ntdll, found at run time with
// GetModuleHandleW and GetProcAddress, so the import table still names only
// kernel32): the port receives a packet when the timer fires. The
// millisecond timeout stays as the bound, because that packet can be
// dequeued by another waiting thread, which then returns early with nothing
// (harmless: PollWait may return early). Go's runtime does the same since
// Go 1.23 (runtime/netpoll_windows.go, golang/go#44343).
//
// There is no thread-local storage, so the timers live in slots: a wait
// takes a free slot by compare-and-swap and gives it back after the wait,
// cancelling the packet's association with RemoveSignaledPacket, so a
// timer that has not fired posts nothing later and a packet already queued
// is taken off the port. STATUS_SUCCESS or STATUS_CANCELLED (the timer
// fired and its packet left the port) leave the slot clean; STATUS_PENDING
// (the timer is firing at that moment) marks it dirty, and the next use
// cancels again first and, while it is still pending, waits with the
// millisecond timeout alone. Any other status, or a timer or packet that
// cannot be made, retires the slot. Without ntdll's three entries (Windows
// before 8) or the high-resolution flag (before Windows 10 1803), every
// wait uses the millisecond timeout alone.

import std.raw

struct HrState {
    // The table page made by PollInit, or 0 when high-resolution waits are
    // unavailable. Written once before any other poll call (PollInit's
    // publication edge: a sequentially consistent store, and every reader
    // loads it after PollInit returned).
    page: raw.Atomic[u64]
    // The slots HrMake made, each holding two handles until exit (a count
    // for TimerHandles only; nothing else reads it).
    made: raw.Atomic[u64]
}

var Hr: HrState = HrState(page: raw.Atomic[u64].New(0), made: raw.Atomic[u64].New(0))

/// TimerHandles is how many handles the high-resolution timer slots hold:
/// two for each slot made (its timer and its wait completion packet), kept
/// until the process exits, at most 2 * HrSlots (128). A slot is made when a
/// timed wait finds every made one busy, dirty or retired, so the count
/// follows the most timed waits at once, not the number of waits. For leak
/// tests (task.KeptHandles).
/// O(1); allocates nothing.
export TimerHandles(): int {
    // Edge: none; a count for leak tests, read with no other datum. Clamped
    // to the table, so a slot made twice could not hide a leaked handle.
    var made := Trunc[int](Hr.made.Load())
    if made > HrSlots {
        made = HrSlots
    }
    return 2 * made
}

// The page: the three ntdll entries at 0, 8 and 16, then HrSlots slots of
// 32 bytes from HrBase: the busy word at 0 (atomic: 0 free, 1 taken), the
// condition at 4 (0 not made, 1 clean, 2 dirty, 3 retired; read and written
// only by the slot's taker), the timer handle at 8, the packet at 16.
const HrCreate: int = 0
const HrAssociate: int = 8
const HrCancel: int = 16
const HrBase: int = 64
const HrSlots: int = 64
const HrSlotBytes: int = 32

// HrKey is the completion key of a timer's packet (it has no OVERLAPPED).
const HrKey: uint = 0x4D4C
// HrMaxNs: from here a timeout uses the millisecond bound alone (an hour;
// the timer's precision matters for short waits).
const HrMaxNs: i64 = 3_600_000_000_000
const TimerHighResolution: u32 = 0x2
const TimerAllAccess: u32 = 0x1F_0003
const GenericAll: u32 = 0x1000_0000
const StatusSuccess: u32 = 0
const StatusPending: u32 = 0x103
const StatusCancelled: u32 = 0xC000_0120

// HrInit finds the ntdll entries and makes the page; called once by
// PollInit. Nothing is made when an entry is missing.
HrInit() {
    var name: [16]u16 = [0; 16]
    _ = Wide("ntdll.dll", mut name[..])
    unsafe "kernel32; the names are NUL-terminated constants" {
        m := GetModuleHandleW(raw.Data(name[..]))
        if m == 0 {
            return
        }
        c := GetProcAddress(m, raw.StrData("NtCreateWaitCompletionPacket\u{0}"))
        a := GetProcAddress(m, raw.StrData("NtAssociateWaitCompletionPacket\u{0}"))
        x := GetProcAddress(m, raw.StrData("NtCancelWaitCompletionPacket\u{0}"))
        if raw.IsNull(c) || raw.IsNull(a) || raw.IsNull(x) {
            return
        }
        p := Reserve(4096, 4096)
        if raw.IsNull(p) || !Commit(p, 4096) {
            return
        }
        Put64(p, HrCreate, u64(raw.Addr(c)))
        Put64(p, HrAssociate, u64(raw.Addr(a)))
        Put64(p, HrCancel, u64(raw.Addr(x)))
        // Edge: the Store publishes the page with its entries, written above,
        // to every Load of Hr.page.
        Hr.page.Store(u64(raw.Addr(p)))
    }
}

// HrSlot is slot i's first byte.
HrSlot(i: int): *u8 {
    unsafe "slot i of the page HrInit made" {
        // Edge: HrInit's Store, made in PollInit before any wait.
        return raw.Offset(raw.FromAddr[u8](Trunc[uint](Hr.page.Load())), HrBase + i * HrSlotBytes)
    }
}

HrBusy(s: *u8): *raw.Atomic[u32] {
    unsafe "the slot's busy word, 4-byte aligned" {
        return raw.PtrCast[u8, raw.Atomic[u32]](s)
    }
}

// HrUnassociate cancels the slot's packet association (removing a queued
// packet) and records the outcome in the slot's condition; true when the
// slot is clean.
HrUnassociate(s: *u8): bool {
    unsafe "the slot is this thread's; the entry is NtCancelWaitCompletionPacket(HANDLE, BOOLEAN): NTSTATUS" {
        // Edge: HrInit's Store, after the entries were written.
        pg := raw.FromAddr[u8](Trunc[uint](Hr.page.Load()))
        var w := Get64(pg, HrCancel)
        cancel := raw.Read(raw.PtrCast[u64, fn(uint, u8): u32](raw.OfMut(mut w)))
        st := cancel(Trunc[uint](Get64(s, 16)), 1)
        if st == StatusSuccess || st == StatusCancelled {
            Put32(s, 4, 1)
            return true
        }
        // Still firing: dirty (2); any other status retires the slot (3).
        Put32(s, 4, if st == StatusPending { 2 } else { 3 })
        return false
    }
}

// HrMake makes slot s's timer and packet; false retires it.
HrMake(s: *u8): bool {
    unsafe "the slot is this thread's; the entry is NtCreateWaitCompletionPacket(PHANDLE, ACCESS_MASK, POBJECT_ATTRIBUTES): NTSTATUS" {
        t := CreateWaitableTimerExW(raw.Null[u8](), raw.Null[u16](), TimerHighResolution, TimerAllAccess)
        if t == 0 {
            Put32(s, 4, 3)
            return false
        }
        // Edge: HrInit's Store, after the entries were written.
        pg := raw.FromAddr[u8](Trunc[uint](Hr.page.Load()))
        var w := Get64(pg, HrCreate)
        create := raw.Read(raw.PtrCast[u64, fn(*u64, u32, *u8): u32](raw.OfMut(mut w)))
        var h: u64 = 0
        if create(raw.OfMut(mut h), GenericAll, raw.Null[u8]()) != StatusSuccess || h == 0 {
            _ = CloseHandle(t)
            Put32(s, 4, 3)
            return false
        }
        Put64(s, 8, u64(t))
        Put64(s, 16, h)
        Put32(s, 4, 1)
        // Edge: none; TimerHandles' count, read with no other datum.
        _ = Hr.made.Add(1)
        return true
    }
}

// HrArm takes a slot and arms its timer to post a packet to the port after
// ns (> 0) nanoseconds. Returns the slot (-1: none, the wait uses the
// millisecond timeout alone) and whether the timer had already fired when
// it was associated (the wait need not block).
HrArm(ns: i64): (slot: int, fired: bool) {
    // A timeout this long needs no precision: the millisecond bound alone
    // (Millis caps it at about 49.7 days, and PollWait may return early).
    // So it takes no slot: PollWait(i64 max) waits on the bound, and a
    // timer is armed only for a wait below HrMaxNs.
    // Edge: HrInit's Store, in PollInit, before any wait.
    if Hr.page.Load() == 0 || ns >= HrMaxNs {
        return (slot: -1, fired: false)
    }
    for i in 0..HrSlots {
        s := HrSlot(i)
        b := HrBusy(s)
        unsafe "slot i of the page HrInit made, which lives until exit; its words are atomics" {
            // busy: the Cas gives the slot to one thread and HrRelease's
            // Store(0) hands it on (the release/acquire edge of a lock, so
            // the condition and handles written by one taker are seen by
            // the next).
            if (*b).Load() != 0 || !(*b).Cas(0, 1) {
                continue
            }
            var cond := Get32(s, 4)
            if cond == 0 && HrMake(s) {
                cond = 1
            }
            // Test point 3: slot 0 behaves as dirty and still pending.
            if i == 0 && cond == 1 && TestOn(3) {
                _ = TestPoint(3)
                cond = 2
            } else if cond == 2 && HrUnassociate(s) {
                cond = 1
            }
            // A slot that is still pending (2) or retired (3) is skipped:
            // the next slot may be clean (taking a pending slot 0 would send
            // every wait to the millisecond timeout).
            if cond != 1 {
                // Edge: the Store(0) releases the slot, as HrRelease's does.
                (*b).Store(0)
                continue
            }
            // Relative due time: negative, in 100 ns units, rounded up so
            // the timer never fires before the timeout (Millis rounds up
            // too); ns < HrMaxNs, so ns + 99 cannot overflow.
            var due: i64 = -((ns + 99) / 100)
            if SetWaitableTimer(Trunc[uint](Get64(s, 8)), raw.OfMut(mut due), 0, raw.Null[u8](), raw.Null[u8](), 0) ==
                0 {
                // Edge: each Store(0) releases the slot to the next taker's
                // Cas.
                (*b).Store(0)
                return (slot: -1, fired: false)
            }
            pg := raw.FromAddr[u8](Trunc[uint](Hr.page.Load()))
            var w := Get64(pg, HrAssociate)
            associate := raw.Read(raw.PtrCast[u64, fn(uint, uint, uint, uint, *u8, u32, uint, *u8): u32](
                raw.OfMut(mut w)))
            var signaled: u8 = 0
            if associate(Trunc[uint](Get64(s, 16)), Port(), Trunc[uint](Get64(s, 8)), HrKey, raw.Null[u8](), 0, 0,
                raw.OfMut(mut signaled)) != StatusSuccess {
                // Edge: the Store(0) releases the slot to the next taker's Cas.
                (*b).Store(0)
                return (slot: -1, fired: false)
            }
            return (slot: i, fired: signaled != 0)
        }
    }
    return (slot: -1, fired: false)
}

// HrRelease ends slot i's wait: its association is cancelled (a timer that
// has not fired posts nothing; a packet still queued is taken off the
// port) and the slot is given back.
HrRelease(i: int) {
    if i < 0 {
        return
    }
    s := HrSlot(i)
    _ = HrUnassociate(s)
    unsafe "the slot's busy word; this thread holds it" {
        (*HrBusy(s)).Store(0)
    }
}
