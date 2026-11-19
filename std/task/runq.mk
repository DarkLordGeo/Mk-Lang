// Why: the scheduler needs per-carrier run queues that the owning carrier
// uses without locks and idle carriers can steal from, plus one shared
// queue for overflow and fairness.
//
// What: each carrier's bounded work-stealing deque (PushLocal, Take,
// StealOne, StealHalf, Overflow), its runnext slot (TakeNext, StealNext),
// and the global queue, the injector (GlobalPush, GlobalSplice, GlobalPop).
//
// How: the deque is the Chase-Lev work-stealing deque on a fixed ring of 256
// entries (D. Chase and Y. Lev, "Dynamic Circular Work-Stealing Deque", SPAA
// 2005), in the form N. M. Le, A. Pop, A. Cohen and F. Zappa Nardelli prove
// correct for weak memory ("Correct and Efficient Work-Stealing for Weak
// Memory Models", PPoPP 2013, figure 1), without the growth step: a full
// push sends half the ring and the new task to the global queue.
// The owner pushes and takes at the bottom (LIFO); thieves take at the top
// (FIFO), one task per compare-and-swap; "steal half" is that many
// single steals (StealHalf).
//
// Memory ordering. raw.Atomic offers only sequentially consistent
// operations, and every load and store of
// top, bottom, the ring slots and runnext below is one. The paper's
// relaxed, acquire and release accesses are each strengthened to seq_cst,
// and its two seq_cst fences are subsumed: Take's store of bottom followed
// by its load of top, and Steal's load of top followed by its load of
// bottom, are seq_cst accesses in program order, so they appear in that
// order in the single total order S of all seq_cst operations, which is the
// store-load ordering the fences exist to give. A strengthening of every
// access keeps every execution the proof allows and removes none of its
// guarantees, so the paper's theorem (each pushed task is taken exactly
// once, by Take or by one successful Steal) holds here. The comments at
// each access name the edge it must create.
//
// ABA. top and bottom are 64-bit counters that only the operations below
// move: top only increases (by a successful compare-and-swap, +1), so a
// compare-and-swap of top from t can succeed only while top still equals t,
// and t is never seen again once passed; 2^63 tasks would have to be stolen
// for it to wrap. The ring index is the counter & 255.

import std.raw
import std.rt

/// RingSize is the capacity of a carrier's deque.
export const RingSize: i64 = 256
const RingMask: i64 = 255

// TaskAt and AddrOf convert between ring words and task records.
TaskAt(x: u64): *Task {
    unsafe "a ring word, runnext or a queue link holds a task record's address, or 0" {
        return raw.FromAddr[Task](Trunc[uint](x))
    }
}

AddrOf(t: *Task): u64 {
    return u64(raw.Addr(t))
}

// RingSlot is slot i & 255 of c's ring.
RingSlot(c: *Carrier, i: i64): *raw.Atomic[u64] {
    unsafe "c is a carrier record; the index is masked into the ring" {
        return raw.Of(&(*c).ring[Trunc[int](i & RingMask)])
    }
}

// PushLocal adds t at the bottom of c's deque. Only c's own thread calls it
// (Chase-Lev's owner), from a task on c or from c's scheduler loop.
PushLocal(c: *Carrier, t: *Task) {
    unsafe "c is the calling thread's own carrier" {
        // Edge: none needed; an owner-only word: this load reads c's own
        // last store of bottom (program order).
        b := (*c).bottom.Load()
        // Edge: this load of top reads the latest successful steal, so the
        // fullness test below never lets a push overwrite a slot a thief
        // may still read (a stale smaller top would only make it stricter).
        t0 := (*c).top.Load()
        if b - t0 >= RingSize {
            Overflow(c, t)
            return
        }
        // Edge (the paper's release before publishing bottom): the slot
        // store precedes the store of bottom in S, so a thief whose load of
        // bottom sees b + 1 then reads this slot's new value.
        (*RingSlot(c, b)).Store(AddrOf(t))
        Jitter(JitterPush)
        (*c).bottom.Store(b + 1)
    }
}

// Take removes the task at the bottom of c's deque, or returns null. Only c's
// own thread calls it.
Take(c: *Carrier): *Task {
    unsafe "c is the calling thread's own carrier" {
        // Edge: none needed; owner-only, as in PushLocal.
        b := (*c).bottom.Load() - 1
        // Edge (the paper's seq_cst fence between these two accesses): the
        // store of bottom precedes the load of top in S. A thief that read
        // the old bottom has its load of top before this store; one that
        // reads the new bottom sees the slot as gone. With one task left
        // (t == b) both sides may reach for it, and the compare-and-swap of
        // top below decides.
        (*c).bottom.Store(b)
        Jitter(JitterTake)
        t0 := (*c).top.Load()
        if t0 <= b {
            x := (*RingSlot(c, b)).Load()
            if t0 == b {
                // The last task: the owner and any thief race on top.
                // Edge: the last-task race: this compare-and-swap and a
                // thief's are ordered in S on top, and exactly one succeeds.
                got := if (*c).top.Cas(t0, t0 + 1) { x } else { 0 }
                (*c).bottom.Store(b + 1)
                return TaskAt(got)
            }
            return TaskAt(x)
        }
        // Empty: restore bottom to top (b + 1 == t0).
        // Edge: restores the owner-only bottom; a thief that reads it reads
        // top after it (StealOne's order) and sees the deque empty.
        (*c).bottom.Store(b + 1)
        return raw.Null[Task]()
    }
}

// StealOne takes the task at the top of v's deque from any thread, or
// returns null when v's deque is empty or another taker won the race (the
// paper's ABORT; the caller moves on).
StealOne(v: *Carrier): *Task {
    unsafe "v is a carrier record; its deque is read only through atomics" {
        // Edge: top is loaded before bottom in S (the paper's seq_cst fence
        // between them), so t0 < b means slot t0 held a pushed task that had
        // not been taken when bottom was read.
        t0 := (*v).top.Load()
        Jitter(JitterSteal)
        b := (*v).bottom.Load()
        if t0 >= b {
            return raw.Null[Task]()
        }
        // The slot is read before the compare-and-swap; it can be
        // overwritten only by a push at index t0 + 256, which needs top >
        // t0 first, and then this compare-and-swap fails and x is dropped.
        x := (*RingSlot(v, t0)).Load()
        // Edge: the steal's transfer: only the compare-and-swap that moves
        // top from t0 gets slot t0's task (the owner's Take races on the
        // same word).
        return if !(*v).top.Cas(t0, t0 + 1) { raw.Null[Task]() } else { TaskAt(x) }
    }
}

// Size is bottom - top as last seen: a hint, exact only to the owner.
Size(c: *Carrier): i64 {
    unsafe "c is a carrier record" {
        // Edge: none; a hint (AnyWork reads it after raising nidle, so a push
        // before that Add is seen: the wake protocol in sched.mk).
        n := (*c).bottom.Load() - (*c).top.Load()
        return if n < 0 { 0 } else { n }
    }
}

// StealHalf moves up to half (rounded up, at most 128) of v's deque to c's:
// the first task is returned, the rest pushed on c's own deque. c is the
// calling thread's carrier and its deque is empty (it is looking for work).
StealHalf(c: *Carrier, v: *Carrier): *Task {
    n := Size(v)
    if n == 0 {
        return raw.Null[Task]()
    }
    var want := n - n / 2
    if want > 128 {
        want = 128
    }
    var first := raw.Null[Task]()
    for _ in 0..Trunc[int](want) {
        x := StealOne(v)
        if raw.IsNull(x) {
            break
        }
        if raw.IsNull(first) {
            first = x
        } else {
            PushLocal(c, x)
        }
    }
    return first
}

// Overflow takes a push to c's full ring: half of it (taken from the top, the oldest,
// with the thieves' own compare-and-swap, so a concurrent thief and this
// owner never both get a task) and t go to the global queue in one locked
// splice. Only the owner pushes, so bottom does not move during the
// loop; the ring cannot be refilled under it. The order inside the batch
// (t first, Go puts it last) does not survive GlobalPop, which pushes all
// but the first task of a batch on the taker's LIFO deque; the fairness
// tick bounds the wait of any of them (test "RT-MN-Fair").
Overflow(c: *Carrier, t: *Task) {
    unsafe "c is the calling thread's own carrier; the tasks are runnable" {
        (*t).link = raw.Null[Task]()
        var head := t
        var tail := t
        var n: u32 = 1
        for _ in 0..Trunc[int](RingSize / 2) {
            x := StealOne(c)
            if raw.IsNull(x) {
                break
            }
            (*x).link = raw.Null[Task]()
            (*tail).link = x
            tail = x
            n += 1
        }
        // A pinned carrier's tasks are its own: they go to its inbox, which
        // only it takes from (PinSplice, carrier.mk), never to the global
        // queue.
        if raw.IsNull((*c).home) {
            GlobalSplice(head, tail, n)
        } else {
            PinSplice(c, head, tail, n)
        }
    }
}

// TakeNext empties c's runnext slot; any thread may race it, and the
// swap gives the task to exactly one.
TakeNext(c: *Carrier): *Task {
    unsafe "c is a carrier record" {
        // Edge: none needed; an early out before the Swap, which transfers.
        if (*c).runnext.Load() == 0 {
            return raw.Null[Task]()
        }
        // Edge: the swap is the transfer of the task; a thief's
        // compare-and-swap (StealNext) and this swap are ordered in S, and
        // only the first sees the address.
        return TaskAt((*c).runnext.Swap(0))
    }
}

// StealNext takes v's runnext from another carrier when it still holds x,
// the value the thief read before it waited (StealRounds). A task's address
// in runnext is the task itself, so a compare-and-swap that succeeds after
// the slot was emptied and refilled with the same task still transfers
// exactly the task the slot holds (the value is the identity; no ABA
// hazard): that task is runnable and in the slot, wherever it was between.
StealNext(v: *Carrier, x: u64): *Task {
    unsafe "v is a carrier record" {
        // Edge: the compare-and-swap and the owner's swap (TakeNext,
        // ReadyPut) are ordered in S; only the first to see x gets the task.
        return if x == 0 || !(*v).runnext.Cas(x, 0) { raw.Null[Task]() } else { TaskAt(x) }
    }
}

// The global queue: a FIFO of tasks linked through Task.link under the
// leaf lock glock (last in the lock order), with its length in glen so the
// scheduler can look without the lock.

GlobalPush(t: *Task) {
    unsafe "t is runnable and on no other list" {
        (*t).link = raw.Null[Task]()
    }
    GlobalSplice(t, t, 1)
}

// GlobalSplice appends the list head..tail of n tasks.
GlobalSplice(head: *Task, tail: *Task, n: u32) {
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit; the global queue under glock" {
        rt.Lock(&(*st).glock)
        if raw.IsNull((*st).gtail) {
            (*st).ghead = head
        } else {
            (*(*st).gtail).link = head
        }
        (*st).gtail = tail
        // Edge: glen is raised after the tasks are linked and before the
        // lock is released; a carrier that reads glen > 0 then takes glock,
        // which orders it after this splice. An idle carrier's recheck
        // reads glen after raising nidle (sched.mk, Idle).
        _ = (*st).glen.Add(n)
        rt.Unlock(&(*st).glock)
    }
}

// GlobalPop takes up to max tasks from the global queue: the first is
// returned, the rest pushed on c's deque.
GlobalPop(c: *Carrier, max: u32): *Task {
    st := TS()
    unsafe "the TaskState page Start mapped, which lives until exit; the global queue under glock" {
        // Edge: a nonzero glen was raised after the tasks were linked under
        // glock (GlobalSplice); this pop takes glock before it reads the list.
        if (*st).glen.Load() == 0 {
            return raw.Null[Task]()
        }
        rt.Lock(&(*st).glock)
        first := (*st).ghead
        if raw.IsNull(first) {
            rt.Unlock(&(*st).glock)
            return first
        }
        var n: u32 = 1
        var last := first
        for n < max && !raw.IsNull((*last).link) {
            last = (*last).link
            n += 1
        }
        (*st).ghead = (*last).link
        if raw.IsNull((*st).ghead) {
            (*st).gtail = raw.Null[Task]()
        }
        (*last).link = raw.Null[Task]()
        // Edge: under glock, as GlobalSplice's Add.
        _ = (*st).glen.Add(0 -% n)
        rt.Unlock(&(*st).glock)
        var x := (*first).link
        (*first).link = raw.Null[Task]()
        for !raw.IsNull(x) {
            next := (*x).link
            (*x).link = raw.Null[Task]()
            PushLocal(c, x)
            x = next
        }
        return first
    }
}
