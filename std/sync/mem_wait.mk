// Why: sync.Semaphore and sync.Notify queue their waiters without allocating,
// which takes raw pointers to cells on the waiters' stacks; keeping every
// unsafe block here leaves semaphore.mk and notify.mk free of them.
//
// What: the Waiter cell and WaitQ, a doubly linked FIFO of cells (QPush,
// QPushFront, QRemove, QPop, QTake), the cell accessors (WNeed, WSince,
// WStarving, WRearm...), and the wake protocol (WaitCell, WakeList).
//
// How: a cell's owner spins, then parks, on the cell's state word; the waker
// unlinks the cell under the primitive's spin lock and then moves the state
// to CellWoken with one Swap, after which it touches the cell no more.

// A cell is linked into a queue, and its fields other than state are read
// and written, only by the holder of the primitive's spin lock
// (core.SpinCore). Its owner spins, then parks, on state, which the waker
// moves to CellWoken once, after it took the cell off the queue (rule W1).
// After that Swap the waker reads nothing of the
// cell: its owner may return at once and its frame be reused, so the
// address is only the key of the wake that follows, and the Io the waker
// wakes with was read before the store (rule W2). The owner parks with a
// Cas from CellSpin to CellParked, and the waker's Swap reads which state it
// replaced, so a wake that comes before the park is never lost (rule W3).

import std.core
import std.raw

struct Waiter {
    next: *Waiter
    prev: *Waiter
    // The waker's list of cells it unlinked and wakes after its unlock.
    wake: *Waiter
    state: raw.Atomic[u32]
    io: Io
    // Semaphore: permits still owed to this waiter, when it first queued
    // (IoTable.now, nanoseconds), and whether it is starving (it has waited
    // SemStarveNanos and lost a retry; semaphore.mk).
    need: int
    since: i64
    queued: bool
    starving: bool
}

struct WaitQ {
    head: *Waiter
    tail: *Waiter
}

WaiterNew(io: Io, need: int): Waiter {
    return Waiter(next: raw.Null[Waiter](), prev: raw.Null[Waiter](), wake: raw.Null[Waiter](),
        state: raw.Atomic[u32].New(0), io: io, need: need, since: 0, queued: false, starving: false)
}

WaitQNew(): WaitQ {
    return WaitQ(head: raw.Null[Waiter](), tail: raw.Null[Waiter]())
}

// The queue under a held spin lock.
WaitQAt(p: *core.SpinCore[WaitQ]): mut WaitQ {
    return core.SpinMut(p)
}

// QPush appends w to q. O(1). The lock is held.
QPush(q: mut WaitQ, w: *Waiter) {
    unsafe "w is the caller's own live cell, not yet queued; the lock is held" {
        (*w).prev = q.tail
        (*w).next = raw.Null[Waiter]()
        (*w).queued = true
    }
    if raw.IsNull(q.tail) {
        q.head = w
    } else {
        unsafe "q.tail is a queued cell, alive by W1; the lock is held" {
            (*q.tail).next = w
        }
    }
    q.tail = w
}

// QPushFront puts w first in q: a woken waiter that lost its retry keeps
// its place (Go's LIFO requeue of a woken waiter). O(1). The lock is held.
QPushFront(q: mut WaitQ, w: *Waiter) {
    unsafe "w is the caller's own live cell, not queued; the lock is held" {
        (*w).prev = raw.Null[Waiter]()
        (*w).next = q.head
        (*w).queued = true
    }
    if raw.IsNull(q.head) {
        q.tail = w
    } else {
        unsafe "q.head is a queued cell, alive by W1; the lock is held" {
            (*q.head).prev = w
        }
    }
    q.head = w
}

// QRemove unlinks w, which is queued in q. O(1). The lock is held.
QRemove(q: mut WaitQ, w: *Waiter) {
    unsafe "w and its neighbours are queued cells, alive by W1; the lock is held" {
        prev := (*w).prev
        next := (*w).next
        if raw.IsNull(prev) {
            q.head = next
        } else {
            (*prev).next = next
        }
        if raw.IsNull(next) {
            q.tail = prev
        } else {
            (*next).prev = prev
        }
        (*w).queued = false
    }
}

// QPop unlinks and returns the first cell, or null. The lock is held.
QPop(q: mut WaitQ): *Waiter {
    w := q.head
    if !raw.IsNull(w) {
        QRemove(q, w)
    }
    return w
}

// QTake unlinks every cell, chains them through wake in queue order and
// returns the first, or null: NotifyAll's batch. O(n). The lock is held.
QTake(q: mut WaitQ): *Waiter {
    first := q.head
    var w := first
    for !raw.IsNull(w) {
        unsafe "w is a queued cell, alive by W1; the lock is held" {
            (*w).queued = false
            (*w).wake = (*w).next
            w = (*w).next
        }
    }
    q.head = raw.Null[Waiter]()
    q.tail = raw.Null[Waiter]()
    return first
}

WQueued(w: *Waiter): bool {
    unsafe "w is a live cell; the lock is held, or w is the caller's own" {
        return (*w).queued
    }
}

WNeed(w: *Waiter): int {
    unsafe "w is a live cell; the lock is held" {
        return (*w).need
    }
}

WSetNeed(w: *Waiter, n: int) {
    unsafe "w is a queued cell, alive by W1; the lock is held" {
        (*w).need = n
    }
}

// WSince and WSetSince: the cell's first queueing time; only its owner
// reads or writes it, before it queues and after it is woken.
WSince(w: *Waiter): i64 {
    unsafe "w is the caller's own cell" {
        return (*w).since
    }
}

WSetSince(w: *Waiter, t: i64) {
    unsafe "w is the caller's own cell, not queued" {
        (*w).since = t
    }
}

WStarving(w: *Waiter): bool {
    unsafe "w is a live cell; the lock is held, or w is the caller's own" {
        return (*w).starving
    }
}

WSetStarving(w: *Waiter, b: bool) {
    unsafe "w is the caller's own cell, not queued, or a queued one under the lock" {
        (*w).starving = b
    }
}

// WRearm makes the caller's own cell, woken and off the queue, ready to
// queue again: no waker holds it (W1), so a plain reset of the state
// orders nothing.
WRearm(w: *Waiter) {
    // Edge: none; no waker holds the cell (W1).
    WState(w).Store(CellSpin)
}

// WSetWake chains w to next on the waker's list. The lock is held, and w
// is unlinked but not yet woken, so its owner waits (W1).
WSetWake(w: *Waiter, next: *Waiter) {
    unsafe "w is an unlinked cell not yet woken: its owner waits for it" {
        (*w).wake = next
    }
}

WState(w: *Waiter): &raw.Atomic[u32] {
    unsafe "w is a live cell: its owner leaves only on CellWoken, or after unlinking it under the lock" {
        return &(*w).state
    }
}

// A cell's state: CellSpin while its owner spins on it, CellParked once
// the owner has said it will park, CellWoken once the waker is done with
// it. The owner moves it from CellSpin to CellParked (a Cas), the waker to
// CellWoken (a Swap), each once.
const CellSpin: u32 = 0
const CellWoken: u32 = 1
const CellParked: u32 = 2

// CellSpins bounds the owner's spin on its own cell before it parks (Rust
// std's and sync_mutex.mk's 100 loads). A handoff that comes within the
// spin costs the waker no wake call and the owner no park: the strict FIFO
// handoff of the semaphore stays, without a cross-carrier wake per permit.
// The loads touch only the
// owner's cell, so they take no line from the lock or the word.
const CellSpins = 100

// WakeList wakes every cell of a list WSetWake or QTake built, each once,
// after the waker's unlock. For each cell: read its next link and its Io,
// then Swap(CellWoken), then wake only when the owner had parked; after
// the Swap nothing of the cell is read (W2), and the address is only the
// wake's key. Release edge: the Swap publishes what the waker did under
// the lock for this cell (the permits it gave, the notification it took)
// to the owner's Load that reads CellWoken (WaitCell's acquire). A Swap,
// not a Store, so the waker learns whether the owner parked: when it read
// CellSpin the owner has not yet passed its Cas, which now fails, so the
// owner never parks and needs no wake (no lost wakeup, W3).
WakeList(first: *Waiter) {
    var w := first
    for !raw.IsNull(w) {
        next: *Waiter
        io: Io
        unsafe "w is unlinked and not yet woken, so its owner is still waiting and the cell is alive (W1)" {
            next = (*w).wake
            io = (*w).io
        }
        st := WState(w)
        if st.Swap(CellWoken) == CellParked {
            core.SyncWake(io, st, false)
        }
        w = next
    }
}

// WaitCell waits until w's state is CellWoken: it spins CellSpins loads,
// then Cas-es CellSpin to CellParked and parks while the state reads
// CellParked. True when the wait was cancelled first (the state may still
// be CellSpin or CellParked: the caller then settles the cell under the
// lock, and may wait again with a NoCancel Io, which parks at once on a
// CellParked state). A spurious return waits again. Acquire edge: the Load
// (or the failed Cas) that reads CellWoken reads the waker's Swap
// (WakeList's release), so everything the waker did for this cell under
// the lock happens before the caller's return. W3: the Cas to CellParked
// and the waker's Swap are on one word, so one of them is first in its
// modification order. If the Swap is, the Cas fails and the owner returns
// without parking; if the Cas is, the Swap reads CellParked and wakes, and
// the owner's wait, which parks only while the word reads CellParked,
// either sees CellWoken or is woken.
WaitCell(io: Io, w: *Waiter): bool {
    st := WState(w)
    var spins := CellSpins
    for {
        s := st.Load()
        if s == CellWoken {
            return false
        }
        if s == CellSpin {
            if spins > 0 {
                spins -= 1
                continue
            }
            if !st.Cas(CellSpin, CellParked) {
                continue
            }
        }
        if core.SyncWait(io, st, CellParked) {
            return st.Load() != CellWoken
        }
    }
}

// OncePut writes init's value into a Once whose state its caller moved to
// running: no other task reads v until the state is done (once.mk's rule O1).
OncePut[T](o: &Once[T], x: T) {
    unsafe "the caller holds the Once's running state, so it is the only task that reaches v, which holds none" {
        raw.Write(raw.Of(&o.v), some(x))
    }
}
