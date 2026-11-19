# Synchronization benchmark rows

Rows of `tools/bench` (the idiomatic standard library table of tests/bench/README.md) for the synchronization primitives of std.sync and std.atomic. Each row directory holds `bench.json` and the Mk package under `mk/`. Every row is a concurrency row: mask 0x5555, 8 threads (notify and notifypermit: mask 0x5, 2 threads), Mk tasks on 8 carriers (notify and notifypermit: 2) against Go goroutines and Rust and Zig OS threads, labelled by model.

## semaphore

`sync.Semaphore` (Go sync.Mutex's two modes over tokio's batch semaphore). `semaphore <n> <t>`: t workers share a semaphore of 4 permits; each does n rounds of acquire 1, an atomic in-use check (a violation when more than 4 hold one), release 1. Prints t * n and the violations (0). Go's std has no semaphore: its idiom, a buffered channel of capacity 4, stands in (weight 1 only). Zig's `std.Io.Semaphore` is weight 1. Rust's std and C's have none.

## barrier

`sync.Barrier` (a generation barrier). `barrier <r> <t>`: t workers wait r rounds at one barrier of t; each counts the rounds it led. Prints the leaders (r) and r. Only Rust's std has a barrier (`std::sync::Barrier`).

## once

`sync.Once`. `once <n> <t>`: t workers each get the value of one shared `Once[i64]` n times (the first get runs an init that returns 7) and add it. Prints 7 * t * n. Go `sync.OnceValue`, Rust `std::sync::OnceLock`; Zig 0.17's std has no once primitive. n is 10^9, so that every column runs for most of a second.

## notify

`sync.Notify` (tokio's Notify) against the condition variables it replaces. `notify <n>`: two workers alternate n times, each waking the other. Mk: one `Notify` per side; the first does `NotifyOne` on the other's then `Wait` on its own, the second the reverse; a notification that finds nobody waiting is a stored permit, so there is no lock and no turn variable. Go (`sync.Mutex` and `sync.Cond`), Rust (`std::sync::Mutex` and `Condvar`) and Zig (`std.Io.Mutex` and `std.Io.Condition`): a mutex holding whose turn it is and one condition variable per side; each side locks, waits while it is not its turn, gives the turn away, unlocks and signals the other. Prints the total rounds (2 * n). Two threads on mask 0x5, as the chan row. C: C11 `<threads.h>`, an `mtx_t` and one `cnd_t` per side, as the Rust column.

The notify row is the condition-variable comparison: it sets Notify beside the primitive it replaces, a mutex, a turn and a condition per side, which is not like with like. The notifypermit row is the like-for-like one.

## notifypermit

`sync.Notify` against each language's permit-style primitive. `notifypermit <n>`: the notify row's workload and the same Mk program. Rust: `std::thread::park` and `Thread::unpark` (unpark stores one token, park consumes it) over an `AtomicBool` flag per side, the pattern of park's documentation, since park may return spuriously. Go: one `chan struct{}` of capacity 1 per side, notify a non-blocking send, wait a receive. Zig: one `std.Io.Event` per side, notify `set`, wait `waitUncancelable` then `reset` (no reset races a wait: the other side sets the event again only after this side's next notify). C: skipped, standard C has no permit primitive (C11 `<threads.h>` has `mtx_t` and `cnd_t` only, which the notify row compares). No side has a lock or a turn variable. Prints the total rounds (2 * n). Two threads on mask 0x5, as the notify row.

## atomicadd

`atomic.AtomicInt.Add`. `atomicadd <n> <t>`: t workers each add 1 to one shared 64-bit atomic counter n times, against Rust `AtomicI64::fetch_add`, Go `atomic.Int64.Add`, Zig `std.atomic.Value(i64).fetchAdd` and C11 `atomic_fetch_add` over `<threads.h>`, every one sequentially consistent (on x86-64 every order of a read-modify-write is the same lock-prefixed instruction). Mk's Add also checks for overflow and returns the new value, so it is a `lock xadd` where a column that drops the result may compile to `lock add`. Prints t * n. Mask 0x5555, 8 threads.

## atomicmax

`atomic.Atomic[i64]`. `atomicmax <n> <t>`: t workers each take a ticket n times with a relaxed fetch-and-add on one shared counter, then raise one shared maximum to the ticket with a weak compare-exchange loop, relaxed on success and failure: the lock-free maximum, with both orders written out. Against C11 `atomic_fetch_add_explicit` and `atomic_compare_exchange_weak_explicit` over `<threads.h>`, Rust `AtomicI64::fetch_add` and `compare_exchange_weak`, Zig `std.atomic.Value(i64).fetchAdd` and `cmpxchgWeak` (`.monotonic`), and Go `atomic.Int64.Add` and `CompareAndSwap` (Go has no orders and no weak CAS; on x86-64 each is the same lock-prefixed instruction). Prints t * n and t * n - 1. Mask 0x5555, 8 threads.
