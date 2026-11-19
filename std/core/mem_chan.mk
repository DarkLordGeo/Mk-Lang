// Why: channels, mutexes and sync.Select share state between tasks through
// raw memory; this file holds every raw field access they need, so
// sync_chan.mk and sync_mutex.mk stay free of most unsafe code.
//
// What: ChanState, ChanMeta, the waiter cells ChanWaiter and MutexWaiter
// with their queues, SelPark, SelCell and SelQ for sync.Select, and the
// accessors over them (ChanAlloc, ChanMetaGet/Put, WStateAt, MQAppend,
// SelCellAt and the rest).
//
// How: a ChanState lives in memory from the channel's allocator and is
// reached only through a raw pointer, never as a mut reference, because
// other tasks touch its atomic words while one holds its lock. Its plain
// fields sit in one ChanMeta that the lock holder reads and writes back
// whole. A waiter cell lives on its parked task's own stack (tasks are
// stackful, so its address is stable while queued) and other tasks touch it
// only under the lock, except its state word.

import std.raw

// The channel's state and waiter cells are declared here, in layer 1,
// because this file's helpers name them (a layer-1 body names no layer-2
// type; TestFreestandingCut). None holds a
// layer-2 field: Io is compiler-known (core_iodecl.mk).
struct ChanState[T] {
    lock: raw.Atomic[u64] // the queue-lock word of Mutex (sync_mutex.mk)
    tab: raw.Atomic[u64]  // TabOf the Io of a task that parked on lock; 0 if none ever did
    flg: raw.Atomic[u64]  // that Io's flags (a Sim or Tape Io names its run there), for TabIo
    m: ChanMeta[T]
    xq: SelQ // the watch cells of parked Selects (sync_chan.mk); the lock holder's
}

// The fields only the lock holder reads or writes. used is the length.
struct ChanMeta[T] {
    closed: bool
    head: int
    used: int
    cap: int
    buf: *T
    rq: WaitQ[T]
    sq: WaitQ[T]
    owner: *Alloc
}

struct WaitQ[T] {
    head: *ChanWaiter[T]
    tail: *ChanWaiter[T]
}

// On the parked task's own stack. state 0 waiting, 1 handed over, 2 closed.
// A receiver's value is none until a sender fills it; a sender's elem is
// the address of its x, null for a receiver.
struct ChanWaiter[T] {
    next: *ChanWaiter[T]
    prev: *ChanWaiter[T]
    state: raw.Atomic[u32]
    io: Io
    elem: *T
    value: ?T
}

/// The state of a parked sync.Select, on the Select's own stack; internal
/// to std. fired goes from 0 to i + 1 when the channel of cell i may have
/// become ready for Recv. A channel sets it under its own lock with a
/// compare-and-swap, so one channel alone fires a park per round and the
/// Select knows which one; the Select passes that wake on as it leaves.
/// io is the Select's Io, through which every wake goes. There is one cell
/// per watched channel.
export struct SelPark {
    fired: raw.Atomic[u32]
    io: Io
    cells: [64]SelCell
}

/// How many channels one sync.Select can watch by parking; a Select over
/// more channels polls instead.
export const SelCells: int = 64

/// One watch of one channel by one sync.Select, inside its SelPark and
/// linked into that channel's queue of watchers; internal to std. Only the
/// holder of that channel's lock reads or writes its fields. queued is
/// true exactly while the cell is linked; at is its index in its park.
export struct SelCell {
    next: *SelCell
    prev: *SelCell
    park: *SelPark
    queued: bool
    at: u32
}

struct SelQ {
    head: *SelCell
    tail: *SelCell
}

// ChanAlloc allocates and initializes a ChanState with a ring of capacity
// slots (none at capacity 0, and a dangling non-null pointer for a
// zero-size T) from a; null when either allocation fails or the ring's
// size overflows int, and nothing is left allocated then. capacity >= 0
// (the caller checked).
ChanAlloc[T](a: &Alloc, capacity: int): *ChanState[T] {
    sp := AllocBytes(a, SizeOfT[ChanState[T]](), AlignOfT[ChanState[T]]())
    if raw.IsNull(sp) {
        return raw.Null[ChanState[T]]()
    }
    var buf := raw.Null[T]()
    if capacity > 0 {
        size := SizeOfT[T]()
        if size == 0 {
            buf = Dangling[T]()
        } else {
            bp := if capacity <= 9223372036854775807 / size { AllocBytes(a, capacity * size, AlignOfT[T]()) } else { raw.Null[u8]() }
            if raw.IsNull(bp) {
                FreeBytes(a, sp, SizeOfT[ChanState[T]](), AlignOfT[ChanState[T]]())
                return raw.Null[ChanState[T]]()
            }
            buf = BytesAs[T](bp)
        }
    }
    st := BytesAs[ChanState[T]](sp)
    nw := raw.Null[ChanWaiter[T]]()
    m := ChanMeta[T](closed: false, head: 0, used: 0, cap: capacity, buf: buf,
        rq: WaitQ[T](head: nw, tail: nw), sq: WaitQ[T](head: nw, tail: nw), owner: raw.Of(a))
    xq := SelQ(head: raw.Null[SelCell](), tail: raw.Null[SelCell]())
    SlotWrite(st, 0, ChanState[T](lock: raw.Atomic[u64].New(0), tab: raw.Atomic[u64].New(0), flg: raw.Atomic[u64].New(0), m: m, xq: xq)) // fresh memory for one ChanState
    return st
}

// ChanFreeState gives the ring and the state back to a, the allocator they
// came from (the caller made the owner check and emptied the ring).
ChanFreeState[T](a: &Alloc, st: *ChanState[T], m: ChanMeta[T]) {
    size := SizeOfT[T]()
    if m.cap > 0 && size > 0 {
        FreeBytes(a, AsBytes(m.buf), m.cap * size, AlignOfT[T]())
    }
    FreeBytes(a, AsBytes(st), SizeOfT[ChanState[T]](), AlignOfT[ChanState[T]]())
}

// The atomic words of the live ChanState at st; nothing is dereferenced.
ChanLockAt[T](st: *ChanState[T]): &raw.Atomic[u64] {
    unsafe "st is a live ChanState: a Chan or a parked task's loan keeps it" {
        return &(*st).lock
    }
}

ChanTabAt[T](st: *ChanState[T]): &raw.Atomic[u64] {
    unsafe "st is a live ChanState: a Chan or a parked task's loan keeps it" {
        return &(*st).tab
    }
}

ChanFlgAt[T](st: *ChanState[T]): &raw.Atomic[u64] {
    unsafe "st is a live ChanState: a Chan or a parked task's loan keeps it" {
        return &(*st).flg
    }
}

// ChanMetaGet and ChanMetaPut copy the plain fields out and back; the
// caller holds the lock.
ChanMetaGet[T](st: *ChanState[T]): ChanMeta[T] {
    unsafe "st is a live ChanState; its lock is held, so no one else writes m" {
        return raw.Read(raw.Of(&(*st).m))
    }
}

ChanMetaPut[T](st: *ChanState[T], m: ChanMeta[T]) {
    unsafe "st is a live ChanState; its lock is held; ChanMeta is plain" {
        raw.Write(raw.Of(&(*st).m), m)
    }
}

// ChanWatched reports whether a Select watches the channel (xq is not
// empty): the one test an operation that may make the channel ready pays
// when nobody watches; the caller holds the lock.
ChanWatched[T](st: *ChanState[T]): bool {
    unsafe "st is a live ChanState; its lock is held, so no one else writes xq" {
        return !raw.IsNull((*st).xq.head)
    }
}

// ChanXqGet and ChanXqPut copy the watch queue out and back, apart from
// ChanMeta so that the operations no Select watches copy only ChanMeta; the
// caller holds the lock.
ChanXqGet[T](st: *ChanState[T]): SelQ {
    unsafe "st is a live ChanState; its lock is held, so no one else writes xq" {
        return raw.Read(raw.Of(&(*st).xq))
    }
}

ChanXqPut[T](st: *ChanState[T], q: SelQ) {
    unsafe "st is a live ChanState; its lock is held; SelQ is plain" {
        raw.Write(raw.Of(&(*st).xq), q)
    }
}

// TabOf is the address of io's table as a word, for ChanState.tab and the
// Select registry; TabIo rebuilds a NoCancel Io from a table word and the
// flags word stored beside it. The flags must travel with the table: a
// Sim's or a Tape's Io names its run in them (slot << 48 | token << 1), and
// an Io rebuilt with other flags is refused by that table's entries.
// Every table is a global, so a stale word
// never names freed memory; a stale run answers Canceled.
TabOf(io: Io): u64 {
    return u64(raw.Addr(io.table))
}

TabIo(t: u64, f: u64): Io {
    unsafe "t is TabOf of a live task's Io, and its table is a global" {
        return Io(table: raw.FromAddr[IoTable](uint(t)), flags: uint(f) | 1)
    }
}

// WaiterOf is the address of a waiter cell on the caller's own stack.
WaiterOf[T](c: mut ChanWaiter[T]): *ChanWaiter[T] {
    return raw.OfMut(c)
}

// ElemOf is the address of a parked sender's x, in its own frame; the
// sender owns x until a receiver has read it and set the cell's state to 1.
ElemOf[T](x: &T): *T {
    return raw.Of(x)
}

// TakeElem moves a parked sender's x out bitwise; the sender then forgets
// its copy (state 1), so the value has one owner again.
TakeElem[T](p: *T): T {
    unsafe "p is a parked sender's x, read once under the lock, then state 1" {
        return raw.Read(p)
    }
}

// ForgetSent ends the sender's copy of a value a receiver took with
// TakeElem: the value has moved, so nothing is dropped.
ForgetSent[T](x: T) {
    unsafe "a receiver moved x out bitwise (TakeElem) and set state 1" {
        raw.Forget(x)
    }
}

// The fields of a queued cell. Its owner is parked, and leaves only after it
// reads a non-zero state or, after a Canceled wait, unlinks the cell under
// the lock, so the cell is alive whenever the lock holder touches it.
WStateAt[T](w: *ChanWaiter[T]): &raw.Atomic[u32] {
    unsafe "w is a queued waiter's live cell" {
        return &(*w).state
    }
}

WNext[T](w: *ChanWaiter[T]): *ChanWaiter[T] {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        return (*w).next
    }
}

WPrev[T](w: *ChanWaiter[T]): *ChanWaiter[T] {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        return (*w).prev
    }
}

WLink[T](w: *ChanWaiter[T], prev: *ChanWaiter[T], next: *ChanWaiter[T]) {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        (*w).prev = prev
        (*w).next = next
    }
}

WSetNext[T](w: *ChanWaiter[T], next: *ChanWaiter[T]) {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        (*w).next = next
    }
}

WSetPrev[T](w: *ChanWaiter[T], prev: *ChanWaiter[T]) {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        (*w).prev = prev
    }
}

WIo[T](w: *ChanWaiter[T]): Io {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        return (*w).io
    }
}

WElem[T](w: *ChanWaiter[T]): *T {
    unsafe "w is a queued waiter's live cell; the lock is held" {
        return (*w).elem
    }
}

// WFill hands x to a queued receiver: its value is none, so nothing is
// dropped (raw.Write). The caller then sets the state to 1.
WFill[T](w: *ChanWaiter[T], x: T) {
    unsafe "w is a queued receiver's live cell whose value is none; the lock is held" {
        raw.Write(raw.Of(&(*w).value), some(x))
    }
}

// RingPut and RingTake move a value into and out of slot i of the ring.
RingPut[T](buf: *T, i: int, x: T) {
    SlotWrite(buf, i, x) // slot i is past the used run, or was just taken
}

RingTake[T](buf: *T, i: int): T {
    return SlotRead(buf, i) // slot i is the head of the used run
}

// The Mutex's waiter cell (sync_mutex.mk), declared here for the same
// reason as ChanWaiter: it lives on its parked task's stack and is threaded
// into the queue whose head the mutex word holds; every field but state is
// read and written only by the holder of the word's queue bit. It is
// 16-aligned, so the low 4 bits of its address are free for the word's
// flags (L, K, W and S, sync_mutex.mk).
align(16) struct MutexWaiter {
    next: *MutexWaiter
    prev: *MutexWaiter
    tail: *MutexWaiter     // the queue's last cell; kept in the head cell only
    state: raw.Atomic[u32] // 0 queued, 1 woken to retry, 2 handed the lock
    io: Io
    starving: bool
}

// MWOf is the address of a cell on the caller's stack; MWAt rebuilds a
// cell's address from the word's queue bits (only a queued cell's address
// is ever stored there, and it stays alive while queued); MWAddr is the
// other way.
MWOf(c: mut MutexWaiter): *MutexWaiter {
    return raw.OfMut(c)
}

MWAt(a: u64): *MutexWaiter {
    unsafe "a is the address of a queued, live MutexWaiter, or 0 for null" {
        return raw.FromAddr[MutexWaiter](uint(a))
    }
}

MWAddr(w: *MutexWaiter): u64 {
    return u64(raw.Addr(w))
}

// MWStateAt is the cell's state word; its owner reads it unlocked.
MWStateAt(w: *MutexWaiter): &raw.Atomic[u32] {
    unsafe "w is a live cell: queued (its owner is parked on it) or its owner's own" {
        return &(*w).state
    }
}

MWIo(w: *MutexWaiter): Io {
    unsafe "w is a queued cell; the queue bit is held" {
        return (*w).io
    }
}

MWStarving(w: *MutexWaiter): bool {
    unsafe "w is a queued cell; the queue bit is held" {
        return (*w).starving
    }
}

MWSetStarving(w: *MutexWaiter, b: bool) {
    unsafe "w is the caller's own cell, not queued" {
        (*w).starving = b
    }
}

// MQAppend, MQPushFront, MQPopFront and MQUnlink edit the queue whose head
// is h (null when empty) and return the new head. O(1) each: the cells are
// doubly linked and the head keeps the tail. The queue bit is held.
MQAppend(h: *MutexWaiter, w: *MutexWaiter): *MutexWaiter {
    unsafe "h and w are live cells; the queue bit is held" {
        (*w).next = raw.Null[MutexWaiter]()
        if raw.IsNull(h) {
            (*w).prev = raw.Null[MutexWaiter]()
            (*w).tail = w
            return w
        }
        t := (*h).tail
        (*t).next = w
        (*w).prev = t
        (*h).tail = w
        return h
    }
}

MQPushFront(h: *MutexWaiter, w: *MutexWaiter): *MutexWaiter {
    unsafe "h and w are live cells; the queue bit is held" {
        (*w).prev = raw.Null[MutexWaiter]()
        if raw.IsNull(h) {
            (*w).next = raw.Null[MutexWaiter]()
            (*w).tail = w
            return w
        }
        (*w).next = h
        (*w).tail = (*h).tail
        (*h).prev = w
        return w
    }
}

MQPopFront(h: *MutexWaiter): *MutexWaiter {
    unsafe "h is the live head cell; the queue bit is held" {
        n := (*h).next
        if !raw.IsNull(n) {
            (*n).prev = raw.Null[MutexWaiter]()
            (*n).tail = (*h).tail
        }
        return n
    }
}

MQUnlink(h: *MutexWaiter, w: *MutexWaiter): *MutexWaiter {
    if raw.Same(h, w) {
        return MQPopFront(h)
    }
    unsafe "h and w are live queued cells, w not the head; the queue bit is held" {
        p := (*w).prev
        n := (*w).next
        (*p).next = n
        if raw.IsNull(n) {
            (*h).tail = p
        } else {
            (*n).prev = p
        }
        return h
    }
}

// ---- sync.Select's park and watch cells ----
//
// A SelPark lives in a parked Select's frame; its cells are linked into
// channels' xq and touched by other tasks only under that channel's lock,
// except fired, which is atomic. The Select leaves only after it has
// unlinked every cell under each channel's lock (std/sync/select.mk), so a
// cell is alive whenever a lock holder touches it.

/// A Select's park record with 64 unlinked cells and nothing fired, in the
/// caller's frame. O(1); allocates nothing.
export SelParkNew(io: Io): SelPark {
    nc := raw.Null[SelCell]()
    return SelPark(fired: raw.Atomic[u32].New(0), io: io,
        cells: [SelCell(next: nc, prev: nc, park: raw.Null[SelPark](), queued: false, at: 0); 64])
}

/// The index of the cell whose channel fired p this round, or -1. The
/// Select reads it after unlinking every cell under each channel's lock,
/// so every fire of this round happened before the read. The Load is
/// sequentially consistent, an acquire edge with the fire's Store. O(1).
export SelParkFiredBy(p: *SelPark): int {
    return int(SelParkFired(p).Load()) - 1
}

/// The address of a park on the caller's own stack. O(1).
export SelParkOf(p: mut SelPark): *SelPark {
    return raw.OfMut(p)
}

SelParkFired(p: *SelPark): &raw.Atomic[u32] {
    unsafe "p is a live SelPark: its Select is parked or running" {
        return &(*p).fired
    }
}

SelParkIo(p: *SelPark): Io {
    unsafe "p is a live SelPark; io is written once, at SelParkNew" {
        return (*p).io
    }
}

// SelCellAt is cell i of p, 0 <= i < SelCells (the caller checked).
SelCellAt(p: *SelPark, i: int): *SelCell {
    unsafe "p is a live SelPark and i is below SelCells" {
        return raw.Offset(raw.Of(&(*p).cells[0]), i)
    }
}

// The lock of the channel whose xq holds (or may hold) c is held.
SelCellGet(c: *SelCell): SelCell {
    unsafe "c is a live cell; the lock of its channel is held" {
        return raw.Read(c)
    }
}

SelCellPut(c: *SelCell, x: SelCell) {
    unsafe "c is a live cell; the lock of its channel is held; SelCell is plain" {
        raw.Write(c, x)
    }
}
