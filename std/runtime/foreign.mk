// Why: a foreign host or a C callback must be able to hand Mk code an Io
// whose operations it serves itself.
//
// What: runtime.ForeignIo, Foreign (Io, Release), ForeignContext, the
// IoTable types a program's table is written with, the static ForeignTable
// of trampolines and the registry Foreigns.
//
// How: Io keeps its compiler-known layout {table, flags}, so, as std.sim's
// Sim does, the Io's table is ForeignTable and its flags hold a registry
// slot and a token: flags = slot << 48 | token << 1, bits 32 to 47 being
// std.core's mask depth and bit 0 the NoCancel bit. Each trampoline finds
// the slot, checks the token and calls the program's entry. Release takes
// the token away and waits for the entries in flight, so a released
// Foreign's Io answers Canceled. Wakes with no Io reach every live
// Foreign through a word-wake listener ForeignIo registers on first use.

import std.core
import std.raw
import std.sys
import std.task

/// The operation table behind an Io: one fn per operation, each taking
/// the Io first. ForeignIo makes an Io over a table of a program's own.
/// IoTable and ForeignIo are provisional: they may change in a minor
/// release.
export type IoTable = core.IoTable
/// What an entry returns: value, or err with the OS's own code in os.
export type IoResult = core.IoResult
/// An entry's failure kind (None on success).
export type SysErr = core.SysErr
/// The records some table entries fill or take: a stat (IoStat), a
/// network address (IoAddr), a child's stdio (IoChild), and a call entry's
/// arguments (IoCallArgs) and words (IoWords).
export type IoStat = core.IoStat
/// A network address as the udp, sendto, recvfrom and resolve entries take it:
/// ip, port, v6 and the IPv6 zone.
export type IoAddr = core.IoAddr
/// The in-out record of the spawn entry: the child's stdio and inherited handle
/// in, its pipe ends and pid out.
export type IoChild = core.IoChild
/// One call of the call entry: the operation, its route, its handles, bytes and
/// words, and its result.
export type IoCallArgs = core.IoCallArgs
/// The four scalar words of a call-entry call, in and out.
export type IoWords = core.IoWords

/// A table whose every entry does nothing and answers Other (os 0), to
/// copy and fill in: `var t := runtime.InertTable`, then set the entries a
/// host provides.
export const InertTable: IoTable = core.InertIo

// The registry: ForeignSlots slots, each claimed by a Cas of its state from
// 0, then written, then published by its token's store.
const ForeignSlots: int = 256
const ForeignTokenMask: u64 = 0x7FFF_FFFF
// ForeignClosing marks a token word whose Foreign's Release has begun: no
// new entry starts (Begin wants the token exactly), but an entry already in
// flight still finds its ctx (ForeignContext compares the low 31 bits),
// until Release has waited for it and clears the word.
const ForeignClosing: u64 = 0x1_0000_0000

// Each slot is five words, read and written only as raw.Atomic[u64] (W):
// state (0 free, 1 claimed), active (entries in flight), token, ctx and
// table. Plain words in static memory, so the registry is a `var` global.
const SlotState: int = 0
const SlotActive: int = 1
const SlotToken: int = 2
const SlotCtx: int = 3
const SlotTable: int = 4

struct ForeignRegistry {
    next: u64
    s: [1280]u64
}

var Foreigns: ForeignRegistry = ForeignRegistry(next: 0, s: [0; 1280])

// ForeignLive counts the live Foreigns, so the word-wake listener costs one
// load while there are none; ForeignListen is the listener's
// registration: 0 none, 1 being registered, 2 registered (three
// states: a caller that loses the Cas waits for 2).
var ForeignLive: raw.Atomic[u64] = raw.Atomic[u64].New(0)
var ForeignListen: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// ListenForeign registers ForeignWordWake with std.core once per process,
// before the first Foreign's Io exists, and returns once it is registered.
ListenForeign() {
    // Edge: seq_cst; the Cas admits one registrar, whose Store of 2 comes
    // after WakeListen published the listener, and a Load of 2 sees it.
    if ForeignListen.Load() == 2 {
        return
    }
    if ForeignListen.Cas(0, 1) {
        ok := core.WakeListen(ForeignWordWake)
        ForeignListen.Store(2)
        if !ok {
            raw.Fail(8, "runtime.ForeignIo: std.core's word-wake listeners are all taken")
        }
        return
    }
    // Edge: seq_cst; a Load of 2 sees the winner's registration (above).
    for ForeignListen.Load() != 2 {
        sys.ThreadYield()
    }
}

// ForeignWordWake is the word-wake listener (std.core's IoWakeWord calls it
// after the native wake, on the thread that called Wake): it calls each
// live Foreign's wake entry with that Foreign's Io, holding the slot
// (Begin, End) around the call. A wake entry that itself calls
// Atomic.Wake or Waker.Wake reaches this listener again on the same
// thread, which returns at once (MarkWakes), so a table may delegate its
// wake to the native one. One load while no Foreign is live; else
// O(ForeignSlots) loads and sys.ThreadId, plus each wake entry.
ForeignWordWake(addr: *u32, all: bool) {
    // Edge: seq_cst; IoWakeWord's fence orders the caller's store of the
    // word before this load, and ForeignIo's Add comes before the
    // Foreign's Io exists, so a waiter parked through a live Foreign is
    // counted here.
    if ForeignLive.Load() == 0 {
        return
    }
    k := MarkEnter(MarkWakes, sys.ThreadId())
    if k < 0 {
        return
    }
    for i in 0..ForeignSlots {
        // Edge: seq_cst; a token read here is checked again by Begin after
        // it raises the slot's active count. A closing slot is skipped.
        t := W(i, SlotToken).Load()
        if t != 0 && t & ForeignClosing == 0 {
            io := ForeignIoOf(i, t)
            g := Begin(io)
            if g.ok {
                f := g.t.wake
                f(io, addr, all)
                End(g)
            }
        }
    }
    MarkLeave(k)
}

// W is word k of slot i, as an atomic.
W(i: int, k: int): &raw.Atomic[u64] {
    unsafe "Foreigns is static; a u64 has raw.Atomic[u64]'s layout, and every access to these words is atomic" {
        return &*raw.PtrCast[u64, raw.Atomic[u64]](raw.Of(&Foreigns.s[i * 5 + k]))
    }
}

// Next is the token counter, as an atomic.
Next(): &raw.Atomic[u64] {
    unsafe "Foreigns is static; a u64 has raw.Atomic[u64]'s layout" {
        return &*raw.PtrCast[u64, raw.Atomic[u64]](raw.Of(&Foreigns.next))
    }
}

/// A registered foreign operation table. It is linear, so it is always
/// released with Release, after which every copy of its Io answers
/// Canceled.
export linear struct Foreign {
    slot: int
    token: u64
}

/// Makes an Io that runs a program's own operation table, as mem.Custom
/// makes an Alloc over a program's own functions. Each entry receives that
/// Io and finds ctx through ForeignContext. table and what ctx points at
/// must live until Release.
///
/// A wake with no Io (Atomic.Wake, Waker.Wake) calls every live Foreign's
/// wake entry with that Foreign's Io, from whatever thread called Wake, a
/// foreign one included, so a wake entry must not suspend. A wake entry
/// that itself calls Atomic.Wake or Waker.Wake, to reach the native wake,
/// is not called again from inside that call.
///
/// Callable only in unsafe (E1101). Panics with code 8 when 256 Foreigns
/// are live. O(256) at worst to find a slot; allocates nothing.
export ForeignIo(ctx: *u8, table: &core.IoTable): Foreign {
    ListenForeign()
    // Edge: seq_cst; counted before the Io exists (ForeignWordWake's load).
    _ = ForeignLive.Add(1)
    // Edge: none for the count; each caller gets a distinct token. The
    // 31-bit token wraps, skipping 0 (no Foreign's): a stale Io answers
    // Canceled unless 2^31 Foreigns were made since its own and one of them
    // took its slot (the ABA bound).
    var t := (Next().Add(1) +% 1) & ForeignTokenMask
    for t == 0 {
        t = (Next().Add(1) +% 1) & ForeignTokenMask
    }
    for i in 0..ForeignSlots {
        // Edge: the Cas claims the slot; the token's store below publishes
        // ctx and table to every Begin that reads it.
        if W(i, SlotState).Load() == 0 && W(i, SlotState).Cas(0, 1) {
            W(i, SlotCtx).Store(u64(raw.Addr(ctx)))
            W(i, SlotTable).Store(u64(raw.Addr(raw.Of(table))))
            W(i, SlotToken).Store(t)
            return Foreign(slot: i, token: t)
        }
    }
    raw.Fail(8, "runtime.ForeignIo: 256 Foreigns are live")
}

/// The Io of this Foreign. Copies may outlive it, and answer Canceled
/// after Release. O(1); allocates nothing.
export Foreign.Io(&self): Io {
    return ForeignIoOf(self.slot, self.token)
}

/// Ends the Foreign: waits for the entries in flight to return, and frees
/// its slot. No entry of the table runs after this, and every copy of the
/// Io answers Canceled. An entry that is parked, such as a sleep or a wait
/// the table hands to the native Io, delays Release until it returns.
///
/// On a task, Release waits by yielding the task through the scheduler,
/// with backoff, so an entry parked on the same carrier keeps running; off
/// a task, on a foreign thread, it yields the thread. An entry must not
/// release its own Foreign: it would wait for itself for ever, and this is
/// not detected. O(1) besides the wait; allocates nothing.
export Foreign.Release(self) {
    Foreign(slot, token) := self
    // Edge: seq_cst store and load against Begin's seq_cst add and load:
    // either Begin sees the token closing (no longer equal), or this sees
    // its active count. The low bits stay, so an entry in flight keeps
    // finding its ctx through ForeignContext until the wait below ends
    // (when this cleared the token to 0, a wake entry the listener had
    // begun found ctx null on Linux).
    W(slot, SlotToken).Store(token | ForeignClosing)
    var n := 0
    for W(slot, SlotActive).Load() != 0 {
        if !task.YieldTask(n) {
            sys.ThreadYield()
        }
        n += 1
    }
    // Edge: seq_cst; the token is cleared before ctx, so ForeignContext's
    // recheck after its ctx load sees a slot being freed.
    W(slot, SlotToken).Store(0)
    W(slot, SlotCtx).Store(0)
    W(slot, SlotTable).Store(0)
    W(slot, SlotState).Store(0)
    // Edge: seq_cst; uncounted after the slot is free.
    _ = ForeignLive.Add(0xFFFF_FFFF_FFFF_FFFF)
}

/// The ctx that ForeignIo was given for io's Foreign, inside one of its
/// entries or anywhere its Io is live; null for an Io that is no live
/// Foreign's. Inside an entry it is the ctx even while another thread's
/// Release of the Foreign waits for that entry. Outside an entry, a
/// concurrent Release may make the answer stale as soon as it is returned.
/// Callable only in unsafe (E1101). O(1); allocates nothing.
export ForeignContext(io: Io): *u8 {
    // A native Io keeps a pinned carrier in bits 48 to 63 (std.task's
    // pinned half): only an Io over ForeignTable names a slot there.
    if TableWord(io) != TableWord(ForeignIoOf(0, 1)) {
        return raw.Null[u8]()
    }
    f := core.IoFlags(io)
    i := Cast[int](f >> 48)
    t := u64((f >> 1) & uint(ForeignTokenMask))
    // Edge: seq_cst; the low 31 bits match while the Foreign is live or
    // its Release is waiting for the entries in flight (ForeignClosing).
    if i >= ForeignSlots || t == 0 || W(i, SlotToken).Load() & ForeignTokenMask != t {
        return raw.Null[u8]()
    }
    // Edge: seq_cst; the token is read again after ctx: a Release and a
    // reuse of the slot between the two token loads changes it (ForeignIo
    // stores ctx before the new token), so a ctx read between two matching
    // loads is this Foreign's.
    c := W(i, SlotCtx).Load()
    if W(i, SlotToken).Load() & ForeignTokenMask != t {
        return raw.Null[u8]()
    }
    unsafe "the slot's ctx word held the address ForeignIo was given, read while its token matched" {
        return raw.FromAddr[u8](Trunc[uint](c))
    }
}

// Guard is one entry's hold on its slot: ok when the token matched, t the
// program's table.
struct Guard {
    ok: bool
    slot: int
    t: &core.IoTable
}

// Begin holds io's slot for one entry, or gives a guard that is not ok.
Begin(io: Io): Guard {
    f := core.IoFlags(io)
    i := Cast[int](f >> 48)
    t := u64((f >> 1) & uint(ForeignTokenMask))
    if i < ForeignSlots && t != 0 {
        // Edge: seq_cst add then load, against Release's store then load.
        _ = W(i, SlotActive).Add(1)
        if W(i, SlotToken).Load() == t {
            unsafe "the token matched after the active count was raised, so the slot's table lives until End" {
                return Guard(ok: true, slot: i, t: &*raw.FromAddr[core.IoTable](Trunc[uint](W(i, SlotTable).Load())))
            }
        }
        _ = W(i, SlotActive).Add(0xFFFF_FFFF_FFFF_FFFF)
    }
    unsafe "a guard that is not ok never reads t; the inert table is static" {
        return Guard(ok: false, slot: 0, t: &*raw.Of(&core.InertIo))
    }
}

// End lets go of a guard's slot.
End(g: Guard) {
    if g.ok {
        _ = W(g.slot, SlotActive).Add(0xFFFF_FFFF_FFFF_FFFF)
    }
}

// Dead is what an entry of a released Foreign's Io answers: Canceled.
Dead(): core.IoResult {
    return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
}

FOpen(io: Io, a0: str, a1: u32): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.open
    r := f(io, a0, a1)
    End(g)
    return r
}

FRead(io: Io, a0: uint, a1: mut []u8, a2: i64): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.read
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FWrite(io: Io, a0: uint, a1: []u8, a2: i64): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.write
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FSize(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.size
    r := f(io, a0)
    End(g)
    return r
}

FSync(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.sync
    r := f(io, a0)
    End(g)
    return r
}

FClose(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.close
    r := f(io, a0)
    End(g)
    return r
}

FRename(io: Io, a0: str, a1: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.rename
    r := f(io, a0, a1)
    End(g)
    return r
}

FRemove(io: Io, a0: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.remove
    r := f(io, a0)
    End(g)
    return r
}

FListen(io: Io, a0: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.listen
    r := f(io, a0)
    End(g)
    return r
}

FAccept(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.accept
    r := f(io, a0)
    End(g)
    return r
}

FDial(io: Io, a0: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.dial
    r := f(io, a0)
    End(g)
    return r
}

FRecv(io: Io, a0: uint, a1: mut []u8): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.recv
    r := f(io, a0, a1)
    End(g)
    return r
}

FSend(io: Io, a0: uint, a1: []u8): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.send
    r := f(io, a0, a1)
    End(g)
    return r
}

FNow(io: Io): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.now
    r := f(io)
    End(g)
    return r
}

FSleep(io: Io, a0: i64): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.sleep
    r := f(io, a0)
    End(g)
    return r
}

FRandom(io: Io, a0: mut []u8): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.random
    r := f(io, a0)
    End(g)
    return r
}

FYield(io: Io): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.yield
    r := f(io)
    End(g)
    return r
}

FSignal(io: Io, a0: u32): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.signal
    r := f(io, a0)
    End(g)
    return r
}

FWait(io: Io, a0: *u32, a1: u32): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.wait
    r := f(io, a0, a1)
    End(g)
    return r
}

FWake(io: Io, a0: *u32, a1: bool) {
    g := Begin(io)
    if !g.ok {
        return
    }
    f := g.t.wake
    f(io, a0, a1)
    End(g)
}

FMkdir(io: Io, a0: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.mkdir
    r := f(io, a0)
    End(g)
    return r
}

FStat(io: Io, a0: str, a1: mut core.IoStat): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.stat
    r := f(io, a0, a1)
    End(g)
    return r
}

FOpendir(io: Io, a0: str): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.opendir
    r := f(io, a0)
    End(g)
    return r
}

FReaddir(io: Io, a0: uint, a1: mut []u8, a2: mut core.IoStat): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.readdir
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FUdp(io: Io, a0: core.IoAddr): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.udp
    r := f(io, a0)
    End(g)
    return r
}

FSendto(io: Io, a0: uint, a1: []u8, a2: core.IoAddr): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.sendto
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FRecvfrom(io: Io, a0: uint, a1: mut []u8, a2: mut core.IoAddr): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.recvfrom
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FResolve(io: Io, a0: str, a1: mut []core.IoAddr): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.resolve
    r := f(io, a0, a1)
    End(g)
    return r
}

FSpawn(io: Io, a0: []str, a1: mut core.IoChild): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.spawn
    r := f(io, a0, a1)
    End(g)
    return r
}

FProcwait(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.procwait
    r := f(io, a0)
    End(g)
    return r
}

FProckill(io: Io, a0: uint): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.prockill
    r := f(io, a0)
    End(g)
    return r
}

FInherited(io: Io): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.inherited
    r := f(io)
    End(g)
    return r
}

FEnter(io: Io, a0: u64) {
    g := Begin(io)
    if !g.ok {
        return
    }
    f := g.t.enter
    f(io, a0)
    End(g)
}

FLeave(io: Io, a0: u64) {
    g := Begin(io)
    if !g.ok {
        return
    }
    f := g.t.leave
    f(io, a0)
    End(g)
}

FDeadline(io: Io, a0: *u8, a1: i64, a2: u32): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.deadline
    r := f(io, a0, a1, a2)
    End(g)
    return r
}

FStdio(io: Io, a0: u32, a1: []u8): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.stdio
    r := f(io, a0, a1)
    End(g)
    return r
}

FCall(io: Io, a0: *core.IoCallArgs): core.IoResult {
    g := Begin(io)
    if !g.ok {
        return Dead()
    }
    f := g.t.call
    r := f(io, a0)
    End(g)
    return r
}

const ForeignTable = core.IoTable(open: FOpen, read: FRead, write: FWrite, size: FSize, sync: FSync, close: FClose, rename: FRename, remove: FRemove, listen: FListen, accept: FAccept, dial: FDial, recv: FRecv, send: FSend, now: FNow, sleep: FSleep, random: FRandom, yield: FYield, signal: FSignal, wait: FWait, wake: FWake, mkdir: FMkdir, stat: FStat, opendir: FOpendir, readdir: FReaddir, udp: FUdp, sendto: FSendto, recvfrom: FRecvfrom, resolve: FResolve, spawn: FSpawn, procwait: FProcwait, prockill: FProckill, inherited: FInherited, enter: FEnter, leave: FLeave, deadline: FDeadline, stdio: FStdio, call: FCall)

// ForeignIoOf is the Io of slot i with token t over ForeignTable. Every
// Foreign Io is made here, so each names the one address this function
// takes of the constant table (each `&ForeignTable` site may name a copy
// of its own), which ForeignContext compares.
ForeignIoOf(i: int, t: u64): Io {
    unsafe "ForeignTable is a constant, alive for the whole process" {
        return core.MakeIo(raw.Of(&ForeignTable), (Cast[uint](i) << 48) | (uint(t) << 1))
    }
}

// TableWord is io's first word, its table's address (an Io is two words,
// its table's address, then its flags; core_iodecl.mk).
TableWord(io: Io): uint {
    unsafe "io is a local copy of two words" {
        return raw.Read(raw.PtrCast[Io, uint](raw.Of(&io)))
    }
}
