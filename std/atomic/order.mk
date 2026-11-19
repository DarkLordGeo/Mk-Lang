// Why: an atomic operation's memory order must be fixed at compile time and
// checked against the operation, so a wrong order is an error, not a bug.
//
// What: Order and its consts Relaxed, Acquire, Release, AcqRel, SeqCst and
// Unordered; Fence; and the clauses CanLoad, CanStore, CanFence and
// CanModify. No Io: a kernel can use them.
//
// How: an Order is one of the consts and nothing else: it has no public
// field, so no other value can be made. The five C++20 orders are 0 to 4;
// 5 is Unordered, lowered as monotonic, a load or store order only: every
// clause below refuses it where it does not apply. An operation takes its
// order as a const parameter, so the order is part of the instance and
// never decided at run time, and an order the operation cannot have is
// E0706 at the call (a compile-time requires). The clauses are exported
// methods (CanModify is a read-modify-write's), so a function outside
// std.atomic that takes an order as a `const` parameter and passes it on
// can state the same clause, as it must:
// `Ld(a: &atomic.Atomic[i64], const o: atomic.Order): i64 requires
// o.CanLoad() { return a.Load(o) }`.
//
// Each order lowers to the LLVM ordering of its name (relaxed is LLVM's
// monotonic) through std.raw's ordered intrinsics; Atomic[T] in
// atomic_t.mk says which edge each makes.

import std.raw

/// A memory order: how an atomic operation orders the memory accesses
/// around it. Pass one of the consts below as an operation's const
/// argument; no other value can be made. One byte.
export struct Order {
    v: u8
}

/// The operation is atomic, and the word's writes fall in one order that
/// every task agrees on, but it orders no other memory: other memory may be
/// seen in any order around it.
export const Relaxed: Order = Order(v: 0)
/// For a load or read-modify-write: when it reads the value of a Release
/// write, it sees every write made before that write.
export const Acquire: Order = Order(v: 1)
/// For a store or read-modify-write: an Acquire that reads its value sees
/// every write this task made before it.
export const Release: Order = Order(v: 2)
/// Acquire and Release at once, for a read-modify-write or a fence.
export const AcqRel: Order = Order(v: 3)
/// Acquire, Release or both, as the operation allows, and every SeqCst
/// operation of the program falls in one order that all tasks agree on.
export const SeqCst: Order = Order(v: 4)
/// For a load or store only: a load sees some value a store wrote, never a
/// torn one, and nothing is ordered by it. Fence and every
/// read-modify-write refuse it (E0706).
export const Unordered: Order = Order(v: 5)

/// Orders memory without touching a word. o is Acquire, Release, AcqRel or
/// SeqCst; Relaxed and Unordered order nothing, so they are E0706. An
/// Acquire fence after a Relaxed load makes that load an Acquire; a Release
/// fence before a Relaxed store makes that store a Release, as C++20's
/// fences do. O(1); it allocates nothing.
export Fence(const o: Order)
    requires o.CanFence() {
    raw.FenceOrd(o.v)
}

/// Reports whether a load may have this order: Relaxed, Acquire, SeqCst or
/// Unordered, since a load cannot release. Load and the failure order of a
/// compare-exchange require it. Evaluated at compile time at each call.
/// O(1); it allocates nothing.
export Order.CanLoad(self): bool {
    return self.v != 2 && self.v != 3
}

/// Reports whether a store may have this order: Relaxed, Release, SeqCst or
/// Unordered, since a store cannot acquire. Store requires it. O(1); it
/// allocates nothing.
export Order.CanStore(self): bool {
    return self.v != 1 && self.v != 3
}

/// Reports whether a fence may have this order: every order but Relaxed,
/// which orders nothing, and Unordered, which is for a load or store only.
/// Fence requires it. O(1); it allocates nothing.
export Order.CanFence(self): bool {
    return self.v != 0 && self.v != 5
}

/// Reports whether a read-modify-write may have this order: every order
/// but Unordered, which is for a load or store only. Swap, the Fetch
/// operations and both orders of a compare-exchange require it. O(1); it
/// allocates nothing.
export Order.CanModify(self): bool {
    return self.v != 5
}
