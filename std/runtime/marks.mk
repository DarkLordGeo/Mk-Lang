// Why: a fault filter or a wake listener must know whether the current
// thread is already inside one, so a fault or wake inside it does not
// recurse.
//
// What: MarkFilters and MarkWakes, two thread sets, with MarkEnter and
// MarkLeave.
//
// How: each set is MarkSlots words of sys.ThreadId. A thread claims a free
// word by Cas and clears it when it leaves, and only that thread writes its
// own id, so its id is in the set exactly while it is inside. Lock-free.

import std.raw
import std.sys

const MarkSlots: int = 64
const MarkFilters: int = 0
const MarkWakes: int = 1

struct MarkTable {
    ids: [128]u64
}

var Marks: MarkTable = MarkTable(ids: [0; 128])

// MarkWord is word k of the table, as an atomic.
MarkWord(k: int): &raw.Atomic[u64] {
    unsafe "Marks is static; a u64 has raw.Atomic[u64]'s layout, and every access to these words is atomic" {
        return &*raw.PtrCast[u64, raw.Atomic[u64]](raw.Of(&Marks.ids[k]))
    }
}

// MarkEnter adds the calling thread, me, to set (MarkFilters or MarkWakes)
// and gives its word's index, or -1 when me is in the set already (a nested
// call, which the caller turns away). With all MarkSlots words held by
// other threads it yields the thread until one leaves: they are inside a
// filter or a wake entry, which returns. O(MarkSlots); allocates nothing.
MarkEnter(set: int, me: u64): int {
    base := set * MarkSlots
    for k in base..base + MarkSlots {
        // Edge: seq_cst; only this thread stores me, so a word holding it
        // was stored by this thread and is read in program order.
        if MarkWord(k).Load() == me {
            return -1
        }
    }
    for {
        for k in base..base + MarkSlots {
            // Edge: seq_cst; the Cas from 0 gives the word to this thread.
            if MarkWord(k).Load() == 0 && MarkWord(k).Cas(0, me) {
                return k
            }
        }
        sys.ThreadYield()
    }
}

// MarkLeave gives back the word MarkEnter gave (nothing for -1).
MarkLeave(k: int) {
    if k >= 0 {
        // Edge: seq_cst; the word is free for any thread after this store.
        MarkWord(k).Store(0)
    }
}
