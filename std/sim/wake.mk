// Why: a wake with no Io (Atomic.Wake, runtime.Waker.Wake) must still
// reach a task that parked on a word through a Sim's Io, or that task
// would wait for ever.
//
// What: ListenWake and UnlistenWake, which count live Sims and register
// WordWake once per process, and WordWake, the listener.
//
// How: std.core's IoWakeWord calls each word-wake listener after the
// native wake; WordWake sends the wake to each live Sim as SimWake would
// for an Io of that Sim (a tape's slot is skipped, as FanLeave skips it).
// A slot freed or restarted meanwhile ignores it: its Io's token no longer
// matches.

import std.core
import std.raw

// Listening is WordWake's registration: 0 none, 1 being registered, 2
// registered (three states, so a Sim.New that loses the Cas returns only
// once the winner's WakeListen has published the listener). LiveSims
// counts the Sims made and not freed, so the
// listener costs one load while there are none.
var Listening: raw.Atomic[u32] = raw.Atomic[u32].New(0)
var LiveSims: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// ListenWake registers WordWake with std.core, once per process, and
// counts one more live Sim (Sim.New, before the Sim's Io exists). O(1)
// besides waiting for another thread's registration to publish.
ListenWake() {
    // Edge: seq_cst; the Cas admits one registrar, whose Store of 2 comes
    // after WakeListen published the listener, and a Load of 2 sees it.
    if Listening.Load() != 2 {
        if Listening.Cas(0, 1) {
            _ = core.WakeListen(WordWake)
            Listening.Store(2)
        } else {
            // The winner's WakeListen is a few stores: spin on the word.
            for Listening.Load() != 2 {
            }
        }
    }
    // Edge: seq_cst; counted before the Sim's Io exists (WordWake's load).
    _ = LiveSims.Add(1)
}

// UnlistenWake counts one live Sim fewer (Sim.Free, after its slot is
// cleared). O(1).
UnlistenWake() {
    // Edge: seq_cst; the slot is clear, so the listener has nothing to wake.
    _ = LiveSims.Add(0xFFFF_FFFF_FFFF_FFFF)
}

// WordWake is the listener: SimWake on every live Sim's Io. One load while
// no Sim is live; else O(SlotCount), a slot lock each, plus the wakes.
WordWake(addr: *u32, all: bool) {
    // Edge: seq_cst; IoWakeWord's fence orders the caller's store of the
    // word before this load, and Sim.New's Add comes before the Sim's Io
    // exists, so a waiter parked through a live Sim is counted here.
    if LiveSims.Load() == 0 {
        return
    }
    for i in 0..SlotCount {
        LockSlot(i)
        t := SlotToken(i)
        tape := SlotTape(i)
        UnlockSlot(i)
        if t != 0 && !tape {
            SimWake(IoFor(i, t), addr, all)
        }
    }
}
