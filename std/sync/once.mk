// Why: lazily built shared values (a table, a parsed config) must be
// computed once even when many tasks ask for them at the same time.
//
// What: sync.Once[T]: New, Get (runs init on first use, waits while another
// task runs it) and Into. No allocation; the value lives inline.
//
// How: one state word (empty, running, running with waiters, done) moved by
// Cas; waiters park on it and the runner wakes them only if one parked.

// Algorithm: Go's sync.Once fast path (one load of a done state, then the
// value) with the states of Rust's futex Once (library/std/src/sys/sync/
// once/futex.rs): 0 empty, 1 running, 2 running with waiters, 3 done. The
// first caller moves 0 to 1 by a Cas and runs init; others move 1 to 2 and
// park on the state; the runner stores the value, then Swap(3), and wakes
// every parked task only when the Swap read 2.
//
// Invariants (the blocks of tests_waits.mk check each, and say which):
//   O1 init runs at most once: only the task whose Cas(0, 1) succeeded runs
//      it, and the state never returns to 0.
//   O2 No caller sees v before it holds init's value: v is read only after
//      a Load that reads 3, which the runner stores after writing v.
//   O3 No lost wakeup: a waiter parks only while it reads 2, which it or
//      another waiter stored by a Cas from 1, so the runner's Swap reads 2
//      and wakes it; a wait that returns early re-reads the state.
//   O4 Poisoning: init takes no Io and cannot be cancelled, and a panic in
//      it ends the process, so a Once is never left half
//      done; a waiter's cancelled wait returns Canceled and changes nothing.
//
// Memory ordering (raw.Atomic is seq_cst only): the runner's writes,
// init's included, happen before every Get that returns the value. The
// runner's Swap(3) is the release; the Load that reads 3 is the acquire.

import std.core
import std.raw

/// A value of type T made once, by the first Get's init, and shared by
/// every later Get across all tasks. The value lives inside the Once, so
/// it allocates nothing. Send when T is Send, and Share when T is Send and
/// Share.
export struct Once[T] {
    state: raw.Atomic[u32]
    v: ?T
}

// The value is made on one task and shared with every other, so it must
// be Send to be made elsewhere, and Share to be read from many.
unsafe impl[T: Send] Send for Once[T] {}
unsafe impl[T: Send + Share] Share for Once[T] {}

const OnceEmpty: u32 = 0
const OnceRunning: u32 = 1
const OnceWaiting: u32 = 2
const OnceDone: u32 = 3

/// A Once with no value yet. It allocates nothing, so it can initialize a
/// `var` global.
export Once[T].New(): Once[T] {
    return Once[T](state: raw.Atomic[u32].New(OnceEmpty), v: none)
}

/// The value, made by init on the first call. Once the value exists, Get
/// is one load and returns it with no cancellation check, in a cancelled
/// scope too, since it neither waits nor changes anything.
///
/// Before that, Get is a cancellation point. The first caller runs init,
/// once across every task, and every other caller waits for it and sees
/// what init wrote. A cancelled wait returns Canceled and changes nothing.
/// init takes no Io and cannot be cancelled; a panic in init ends the
/// process, so no caller sees a value init did not finish. An init that
/// calls Get on its own Once waits forever. O(1) besides init and the
/// wait; allocates nothing.
export Once[T].Get(&self, io: Io, const init: fn(): T): Canceled!&T from self {
    // Acquire edge: a Load that reads 3 reads the runner's Swap(3), so v
    // holds the value (O2).
    if self.state.Load() == OnceDone {
        return OnceValue(self)
    }
    return try OnceSlow(self, io, init)
}

// OnceSlow is Get before the value exists, out of line so that Get's fast
// path is one load, a compare and the value, small enough to inline.
OnceSlow[T](o: &Once[T], io: Io, const init: fn(): T): Canceled!&T from o {
    st := &o.state
    try core.SyncCancel(io)
    for {
        // Orders nothing a reader relies on: the Cas only picks the runner
        // (O1); the runner's release is its Swap.
        if st.Cas(OnceEmpty, OnceRunning) {
            OncePut(o, init())
            // Release edge: publishes v and init's writes to every Load
            // that reads 3.
            if st.Swap(OnceDone) == OnceWaiting {
                core.SyncWake(io, st, true)
            }
            return OnceValue(o)
        }
        // Acquire edge: as the first Load.
        s := st.Load()
        if s == OnceDone {
            return OnceValue(o)
        }
        // Orders nothing: announces a waiter to the runner's Swap (O3).
        if s == OnceRunning && !st.Cas(OnceRunning, OnceWaiting) {
            continue
        }
        if core.SyncWait(io, st, OnceWaiting) {
            // Acquire edge: as the first Load; done before the cancel
            // landed, so Get succeeds.
            if st.Load() == OnceDone {
                return OnceValue(o)
            }
            fail Canceled
        }
    }
}

/// The value, or none if no Get made it. It takes the Once by value, so no
/// init can be running. O(1); allocates nothing.
export Once[T].Into(self): ?T {
    Once(state: _, v) := self
    return v
}

// OnceValue is the value of a done Once (O2).
OnceValue[T](o: &Once[T]): &T from o {
    match &o.v {
        some(x) {
            return x
        }
        none {
            raw.Fail(8, "Once: done with no value")
        }
    }
}
