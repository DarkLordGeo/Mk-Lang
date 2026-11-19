# std.sync

`std.sync` coordinates tasks beyond what the prelude's `Chan` and `Mutex` do. It has a read-write lock, a semaphore, a signal that cannot be lost, a barrier, one-time initialization, a wait on several channels at once, and a shared reference-counted value. Reach for `Mutex[T]` and `Chan[T]` first. Use `std.sync` when many tasks read and few write (`RwLock`), or when you bound how many do something at once (`Semaphore`). Use it also when a task waits for an event (`Notify`), a round of peers (`Barrier`), a lazily built value (`Once`) or the first of several channels (`Select`). For single atomic words, use [`std.atomic`](atomic.md).

Read the [standard library guide](README.md) first, in particular [cancellation](README.md#6-cancellation), and the reference's [concurrency](../reference.md#12-concurrency) section for tasks and scopes.

```mk
// verdict: parse
import std.sync
```

## Contents

- [The main pieces](#the-main-pieces)
- [Common rules](#common-rules)
- [RwLock](#rwlock)
- [Semaphore](#semaphore)
- [Notify](#notify)
- [Barrier](#barrier)
- [Once](#once)
- [Select](#select)
- [Shared](#shared)
- [Examples](#examples): [many readers, one writer](#many-readers-one-writer), [limit work in flight](#limit-work-in-flight), [build a table once](#build-a-table-once), [wait on several channels](#wait-on-several-channels), [wait for a condition](#wait-for-a-condition)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Share a value that many read and few write | `RwLock[T]`: `Lock` to write, `LockRead` to read |
| Let at most n tasks do something at once | `Semaphore`: `Acquire`, `TryAcquire`, `Release` |
| Wake a task when something happened | `Notify`: `Wait`, `NotifyOne`, `NotifyAll` |
| Make n tasks meet at the end of each round | `Barrier`: `Wait` |
| Build a value once, on first use, for every task | `Once[T]`: `Get` |
| Wait until one of several channels can receive | `Select` |
| Keep a value alive while any task holds it | `Shared[T]`: `New`, `Clone`, `Ref`, `Free` |

## Common rules

- **No allocation.** `RwLock`, `Semaphore`, `Notify`, `Barrier` and `Once` allocate nothing, so each can initialize a `var` global. A waiting task waits in a cell on its own task's stack. Only `Shared` allocates, one box on the heap.
- **Cancellation.** Every call that can wait takes an `Io` and returns `Canceled` when its task's scope is cancelled. The doc of each call says what a cancelled call leaves behind; none leaves a lock held or a permit lost. Calls that never wait (`NotifyOne`, `NotifyAll`, `TryAcquire`, `Release`, the guards' `Unlock`) take no `Io` and are not cancellation points.
- **Ordering.** What a task writes before it releases a lock, a permit or a notification is visible to the task that takes it. What each task writes before a `Barrier.Wait` is visible to every task after the round.
- **What is not here.** There is no `WaitGroup`: a `scope` joins every task it starts. There is no condition variable: a task that waits for a condition under a `Mutex` unlocks, waits on a `Notify`, and locks again. A task that changes the condition under the lock and then calls `NotifyOne` cannot be missed, because the permit stays until the `Wait`. See [wait for a condition](#wait-for-a-condition).

## RwLock

`RwLock[T]` holds a value that many readers or one writer may hold. `Lock` excludes everyone and returns the prelude's `Guard[T]`, the same guard a `Mutex` gives. `LockRead` excludes only `Lock` and returns a `ReadGuard[T]`, whose `Ref` gives shared access. Both guards are linear: `Unlock` ends them, so a lock cannot be forgotten.

Writers are not starved. A waiting writer turns new readers away. The readers and writers that wait queue on one `Mutex`, in order, with its handoff to a waiter that has waited 1 ms or more. More than 2^30 - 1 readers at once panics.

## Semaphore

A `Semaphore` holds a count of permits. `Acquire(io, n)` waits until `n` are free and takes them; `Release(n)` gives `n` back and needs no `Io`; `TryAcquire(n)` takes `n` only when they are free, and never waits.

- A free permit goes to whichever task finds it. A waiter that has waited 1 ms and still finds too few starves. From then on permits go to the waiters in arrival order, and nobody takes one past them, so smaller requests never starve a large one. `TryAcquire` returns false while a waiter starves.
- A cancelled `Acquire` holds no permit: what it was given while it waited goes to the next waiters.
- A `Semaphore` holds at most 2^60 - 1 free permits; more is the overflow panic. An `n` below 0 panics. A request for more permits than will ever be released waits until it is cancelled, and so does every request queued behind it.

## Notify

A `Notify` is a signal that cannot be lost. Neither `NotifyOne` nor `NotifyAll` needs an `Io`.

- `NotifyOne` wakes the first waiter or, when nobody waits, stores one permit, which the next `Wait` consumes at once.
- Two `NotifyOne` calls with nobody waiting store one permit.
- `NotifyAll` wakes every task waiting at that moment and stores nothing.

A `Wait` that a `NotifyOne` reached returns ok even when its scope was cancelled meanwhile, and a cancelled `Wait` consumes nothing. So the usual pattern, a flag and a `Notify`, never misses the event:

```mk
// verdict: ok
// the permit NotifyOne stores covers a waiter that checked the flag and
// has not reached Wait yet
import std.atomic
import std.sync

AwaitDone(done: &atomic.AtomicBool, ev: &sync.Notify, io: Io): Canceled!void {
    for !done.Load() {
        try ev.Wait(io)
    }
}

MarkDone(done: &atomic.AtomicBool, ev: &sync.Notify) {
    done.Store(true)
    ev.NotifyOne()
}
```

## Barrier

`Barrier.New(n)`, with `n` at least 1, holds rounds of `n` arrivals. `Wait` returns once `n` tasks have called it in its round. It returns `true` in the one task whose call completed the round, and the barrier is then ready for the next round. A cancelled `Wait` takes its arrival back, so the round still needs `n`.

```mk
// verdict: ok
import std.sync

// Step runs one phase of a computation in each of n tasks. No task starts
// phase 2 before every task has finished phase 1.
Step(io: Io, b: &sync.Barrier, id: int) {
    // ... phase 1 for id ...
    leader := b.Wait(io) else {
        return // Canceled: the arrival is taken back
    }
    if leader {
        // exactly one task per round gets true: do the serial part here
    }
    // ... phase 2 for id ...
}

Run(io: Io, n: int) {
    b := sync.Barrier.New(n) // n at least 1
    scope s := io.Scope() {
        for id in 0..n {
            s.go Step(io, &b, id)
        }
    }
}
```

## Once

`Once[T].Get(io, init)` runs `init` on the first call, once across every task, and returns the value; the other callers wait for it.

- Once the value exists, `Get` returns it with no cancellation check, since it neither waits nor changes anything.
- `init` takes no `Io` and cannot be cancelled.
- A panic in `init` ends the process, so no caller sees a value `init` did not finish.
- An `init` that calls `Get` on its own `Once` waits forever.
- `Into` takes the value out of a `Once` you own.

## Select

`Select(io, chans)` takes a record whose fields are each a `&Chan[T]` (or a `Chan[T]`). It waits until one of them can `Recv` without waiting: a value buffered, a sender waiting, or the channel closed. It returns that field's index, counted in byte order of the field names, not the order you wrote them; then you call `Recv` on that field.

- The fields are read in that order, then the fields before the one found are read again until none of them is ready. So a field made ready by a `Send` or `Close` before the returned field became ready is returned instead, unless another task received from it meanwhile.
- The first ready field wins. A channel that is always ready, such as a closed one, starves the fields after it, so stop selecting on a channel once it is closed.
- A `Send` wakes one `Select` watching that channel, which passes the wake on as it returns while the channel is still ready; `Close` wakes all.
- `Chan[T]` and `&Chan[T]` implement `Selectable` for every `T`, and nothing else can.
- `Select` allocates nothing. A record of more than 64 fields is polled every 1 ms instead of waited on.

A `TrySend` on a channel of capacity 0 succeeds only against a task waiting in `Recv`; a task waiting in `Select` is not one.

## Shared

`Shared[T]` is a handle on a value in a reference-counted box on the heap, for a value with no single owner, such as a cache entry many tasks read. `New` makes the box and the first handle, `Clone` makes another, `Ref` reads the value, and `Free` ends a handle; the last `Free` drops the value and frees the box. Access is read-only: put a `Mutex` or `RwLock` inside for a value that changes.

Each handle is linear, so the compiler makes you free every one. A last `Free` of a box holding a linear `T` panics, since the value would be dropped unended. A cycle of `Shared` values is never freed: it leaks memory, and nothing unsafe follows.

```mk
// verdict: ok
import std.atomic
import std.sync

struct Table {
    names: [4]str
}

Count(t: sync.Shared[Table], h: &Heap, i: int, total: &atomic.AtomicInt) {
    _ = total.Add(i64(t.Ref().names[i].Len())) // read-only access through any handle
    t.Free(h) // each handle is freed once; the last one frees the box
}

Fanout(sys: &System, total: &atomic.AtomicInt): OutOfMemory!void {
    t := try sync.Shared[Table].New(sys.heap, Table(names: ["a", "bb", "ccc", "dddd"]))
    scope s := sys.io.Scope() {
        for i in 0..4 {
            s.go Count(t.Clone(), sys.heap, i, total) // one more handle on the same box
        }
    }
    t.Free(sys.heap)
}
```

## Examples

Each program below runs as written with `mk run`. Tasks run in parallel, so the programs print only what does not depend on the order in which the tasks ran.

### Many readers, one writer

```mk
// verdict: ok
import std.atomic
import std.sync

struct Config {
    limit: int
    version: int
}

// An RwLock allocates nothing, so it can initialize a global.
var Current: sync.RwLock[Config] = sync.RwLock[Config].New(Config(limit: 10, version: 1))

Reader(io: Io, sum: &atomic.AtomicInt) {
    for _ in 0..1000 {
        r := Current.LockRead(io) else { // many readers hold it at once
            return // Canceled
        }
        _ = sum.Add(i64(r.Ref().limit))
        r.Unlock() // a ReadGuard is linear: Unlock ends it
    }
}

Update(io: Io, limit: int) {
    var g := Current.Lock(io) else { // excludes readers and other writers
        return
    }
    g.Mut().limit = limit
    g.Mut().version += 1
    g.Unlock()
}

export Main(sys: &System): Canceled|WriteError!void {
    sum := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..4 {
            s.go Reader(sys.io, &sum)
        }
        s.go Update(sys.io, 20)
    } // the scope waits for every task it started
    r := try Current.LockRead(sys.io)
    c := *r.Ref() // Config is plain: copy it out, then unlock
    r.Unlock()
    try sys.Print("version {v}: limit {l}\n", (v: c.version, l: c.limit))
    total := sum.Load()
    try sys.Print("each read saw 10 or 20: {ok}\n", (ok: total >= 40_000 && total <= 80_000 && total % 10 == 0))
}
```

```text
$ mk run config.mk
version 2: limit 20
each read saw 10 or 20: true
```

- A task under a scope returns nothing, so a task handles its own errors, here with `else`.
- Copy what you need out of the guard and unlock before slow work, such as printing.

### Limit work in flight

```mk
// verdict: ok
import std.atomic
import std.sync
import std.time

struct Counts {
    busy: atomic.AtomicInt // jobs running now
    over: atomic.AtomicInt // times more than three ran
    done: atomic.AtomicInt
}

Job(io: Io, slots: &sync.Semaphore, c: &Counts) {
    slots.Acquire(io, 1) else { // wait for a free slot
        return // Canceled: no permit is held
    }
    if c.busy.Add(1) > 3 { // Add returns the new value
        _ = c.over.Add(1)
    }
    io.Sleep(time.Millis(10)) else {}
    _ = c.busy.Add(-1)
    _ = c.done.Add(1)
    slots.Release(1) // takes no Io and cannot fail
}

export Main(sys: &System): WriteError!void {
    slots := sync.Semaphore.New(3) // three jobs at a time
    c := Counts(busy: atomic.AtomicInt.New(0), over: atomic.AtomicInt.New(0), done: atomic.AtomicInt.New(0))
    scope s := sys.io.Scope() {
        for _ in 0..12 {
            s.go Job(sys.io, &slots, &c)
        }
    }
    try sys.Print("jobs done: {n}\n", (n: c.done.Load()))
    try sys.Print("more than 3 at once: {n} times\n", (n: c.over.Load()))
}
```

```text
$ mk run limit.mk
jobs done: 12
more than 3 at once: 0 times
```

### Build a table once

```mk
// verdict: ok
import std.atomic
import std.sync

var Builds: atomic.AtomicInt = atomic.AtomicInt.New(0)

// Squares is the table, built on first use.
var Squares: sync.Once[[16]int] = sync.Once[[16]int].New()

Build(): [16]int {
    _ = Builds.Add(1)
    var t: [16]int = [0; 16]
    for i in 0..16 {
        t[i] = i * i
    }
    return t
}

Square(io: Io, i: int, sum: &atomic.AtomicInt) {
    t := Squares.Get(io, Build) else { // the first caller runs Build; the others wait
        return
    }
    _ = sum.Add(i64(t[i]))
}

export Main(sys: &System): WriteError!void {
    sum := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for i in 0..16 {
            s.go Square(sys.io, i, &sum)
        }
    }
    try sys.Print("sum of squares: {n}\n", (n: sum.Load()))
    try sys.Print("built {n} time(s)\n", (n: Builds.Load()))
}
```

```text
$ mk run once.mk
sum of squares: 1240
built 1 time(s)
```

- The value lives inside the `Once`, so `Get` returns a reference that borrows it.

### Wait on several channels

```mk
// verdict: ok
import std.sync

Worker(io: Io, results: &Chan[int], logs: &Chan[str], quit: &Chan[int]) {
    for i in 1..4 {
        results.Send(io, i * 10) else {
            return
        }
        logs.Send(io, "step") else {
            return
        }
    }
    quit.Close() // a closed channel stays ready, so every later Select sees it
}

export Main(sys: &System): OutOfMemory|Canceled|Closed|WriteError!void {
    a := sys.heap.Alloc()
    results := try Chan[int].New(a, 0)
    logs := try Chan[str].New(a, 0)
    quit := try Chan[int].New(a, 0)
    var sum := 0
    var steps := 0
    scope s := sys.io.Scope() {
        s.go Worker(sys.io, &results, &logs, &quit)
        for {
            // Indexes go by field name in byte order: logs 0, quit 1, results 2.
            i := try sync.Select(sys.io, (results: &results, logs: &logs, quit: &quit))
            if i == 0 {
                _ = try logs.Recv(sys.io)
                steps += 1
            } else if i == 1 {
                break
            } else {
                sum += try results.Recv(sys.io)
            }
        }
    }
    results.Free(a)
    logs.Free(a)
    quit.Free(a)
    try sys.Print("sum {sum} after {steps} steps\n", (sum, steps))
}
```

```text
$ mk run select.mk
sum 60 after 3 steps
```

- The channels have capacity 0, so every `Send` waits for its `Recv`, and the worker closes `quit` only after the last value was taken.
- `Select` only reports; the `Recv` that follows takes the value.

### Wait for a condition

```mk
// verdict: ok
import std.sync

struct Box {
    ready: Mutex[int] // how many items are ready
    changed: sync.Notify
}

// WaitFor waits until at least n items are ready. It is the condition
// variable pattern: check under the lock, unlock, wait, lock again.
WaitFor(io: Io, b: &Box, n: int): Canceled!int {
    for {
        g := try b.ready.Lock(io)
        have := *g.Ref()
        g.Unlock()
        if have >= n {
            return have
        }
        try b.changed.Wait(io) // a NotifyOne since the check is not lost
    }
}

Producer(io: Io, b: &Box) {
    for _ in 0..5 {
        var g := b.ready.Lock(io) else {
            return
        }
        *g.Mut() += 1 // change the condition under the lock
        g.Unlock()
        b.changed.NotifyOne() // then notify: takes no Io
    }
}

export Main(sys: &System): Canceled|WriteError!void {
    b := Box(ready: Mutex[int].New(0), changed: sync.Notify.New())
    var seen := 0
    scope s := sys.io.Scope() {
        s.go Producer(sys.io, &b)
        seen = try WaitFor(sys.io, &b, 3)
    }
    try sys.Print("at least 3 ready: {ok}\n", (ok: seen >= 3))
    g := try b.ready.Lock(sys.io)
    total := *g.Ref()
    g.Unlock()
    try sys.Print("ready in the end: {total}\n", (total,))
}
```

```text
$ mk run condition.mk
at least 3 ready: true
ready in the end: 5
```

## API

Generated from the module's doc comments by `mk doc --std std.sync` ([reading the API](README.md#reading-an-api-section)).

<!-- api:begin -->
Stability: Stable.

### `Barrier`

```
export struct Barrier
```

A reusable meeting point for n tasks. Each round ends when n tasks have
called Wait, and the task whose call completed the round is its leader.
The barrier is then ready for the next round. It allocates nothing, and
each waiter waits in a cell on its own task's stack.

### `Barrier.New`

```
export Barrier.New(n: int): Barrier
```

A barrier for rounds of n tasks. It allocates nothing, so it can
initialize a `var` global. An n below 1 panics with code 8.

### `Barrier.Wait`

```
export Barrier.Wait(&self, io: Io): Canceled!bool
```

- Errors: Canceled

Waits until n tasks have called Wait in this round, and returns true in
the one whose call completed it (the leader) and false in the others.
Every write an arrival made before its Wait is visible to every task
after its Wait returns.

A cancellation point first. A Wait cancelled before its round completes
returns Canceled and takes its arrival back, so the round still needs n
arrivals; one whose round completed first returns false. It allocates
nothing.

### `Notify`

```
export struct Notify
```

A signal one task sends and another waits for, which cannot be lost.
NotifyOne wakes the first waiter or, when nobody waits, stores one
permit for the next Wait; NotifyAll wakes every task waiting at that
moment. Use it with a flag or a Mutex in place of a condition variable.
It allocates nothing, and each waiter waits in a cell on its own task's
stack.

### `Notify.New`

```
export Notify.New(): Notify
```

A Notify with no permit and no waiter. It allocates nothing, so it can
initialize a `var` global.

### `Notify.NotifyAll`

```
export Notify.NotifyAll(&self)
```

Wakes every task waiting at the moment of the call and stores no
permit: a Wait that starts later waits for the next notification. Takes
no Io and is not a cancellation point. O(waiters); allocates nothing.

### `Notify.NotifyOne`

```
export Notify.NotifyOne(&self)
```

Wakes the first waiter, or stores one permit for the next Wait when
nobody waits. Permits do not add up: two calls with nobody waiting
store one. The woken waiter sees every write this task made before the
call. Takes no Io and is not a cancellation point. O(1); allocates
nothing.

### `Notify.Wait`

```
export Notify.Wait(&self, io: Io): Canceled!void
```

- Errors: Canceled

Waits for a notification and consumes it: a stored permit at once, else
the next NotifyOne or NotifyAll.

A cancellation point first. A cancelled Wait returns Canceled and
consumes nothing. A Wait that a NotifyOne reached returns ok even when
its scope was cancelled meanwhile, so no notification is lost to a
cancel. O(1) besides the wait; allocates nothing.

### `Once`

```
export struct Once[T]
```

A value of type T made once, by the first Get's init, and shared by
every later Get across all tasks. The value lives inside the Once, so
it allocates nothing. Send when T is Send, and Share when T is Send and
Share.

### `Once.Get`

```
export Once[T].Get(&self, io: Io, const init: fn(): T): Canceled!&T from self
```

- Flows: The result borrows from self.
- Errors: Canceled

The value, made by init on the first call. Once the value exists, Get
is one load and returns it with no cancellation check, in a cancelled
scope too, since it neither waits nor changes anything.

Before that, Get is a cancellation point. The first caller runs init,
once across every task, and every other caller waits for it and sees
what init wrote. A cancelled wait returns Canceled and changes nothing.
init takes no Io and cannot be cancelled; a panic in init ends the
process, so no caller sees a value init did not finish. An init that
calls Get on its own Once waits forever. O(1) besides init and the
wait; allocates nothing.

### `Once.Into`

```
export Once[T].Into(self): ?T
```

- Flows: The result borrows from self.

The value, or none if no Get made it. It takes the Once by value, so no
init can be running. O(1); allocates nothing.

### `Once.New`

```
export Once[T].New(): Once[T]
```

A Once with no value yet. It allocates nothing, so it can initialize a
`var` global.

### `ReadGuard`

```
export linear view struct ReadGuard[T]
```

A held read lock of an RwLock, giving shared access to its value.
Linear: Unlock ends it, as Guard.Unlock ends a write lock, so the lock
cannot be forgotten. It borrows the RwLock while it lives, and no
writer can enter until it is unlocked.

### `ReadGuard.Ref`

```
export ReadGuard[T].Ref(&self): &T
```

- Flows: The result borrows from self.

The value, shared with the other readers. No writer holds a Guard while
this ReadGuard lives. O(1).

### `ReadGuard.Unlock`

```
export ReadGuard[T].Unlock(self)
```

Releases this read lock. When it is the last reader out and a writer
waits, the writer is woken; that writer's Lock sees every read made
under this lock as done. Not a cancellation point. O(1); allocates
nothing.

### `RwLock`

```
export struct RwLock[T]
```

A lock over a value of type T that many readers or one writer hold.
Lock excludes everyone and returns the prelude's Guard; LockRead
excludes only Lock and returns a ReadGuard. Writers are not starved: a
waiting writer turns new readers away, and the readers and writers that
wait queue on one Mutex, served in order, with the same handoff to a
waiter that has waited too long. It allocates nothing. Send when T is
Send, and Share when T is Send and Share.

### `RwLock.Into`

```
export RwLock[T].Into(self): T
```

- Flows: The result borrows from self.

The value. It takes the RwLock by value, so no guard can be holding it.
O(1); allocates nothing.

### `RwLock.Lock`

```
export RwLock[T].Lock(&self, io: Io): Canceled!Guard[T]
```

- Flows: The result borrows from self.
- Errors: Canceled

Takes the write lock, waiting for the writer before it and for the
readers inside to leave, and returns the Guard; Guard.Unlock releases
it. Readers that arrive while a writer waits or holds the lock wait
behind it. Canceled, without the lock, when the scope is cancelled at
the call or while it waits. O(1) besides the wait; allocates nothing.

### `RwLock.LockRead`

```
export RwLock[T].LockRead(&self, io: Io): Canceled!ReadGuard[T]
```

- Flows: The result borrows from self.
- Errors: Canceled

Takes a read lock, beside any other readers, and returns the
ReadGuard. It waits only while a writer holds or waits for the lock.
A cancellation point first, as Mutex.Lock is: in a cancelled scope it
returns Canceled at once, and a cancel while it waits returns Canceled
without the lock. More than 2^30 - 1 readers at once panics with code
1. O(1) besides the wait; allocates nothing.

### `RwLock.New`

```
export RwLock[T].New(x: T): RwLock[T]
```

- Flows: The result borrows from x.

An unlocked RwLock holding x. It allocates nothing, so it can
initialize a `var` global.

### `Select`

```
export Select[R: Fields[Selectable] + Plain](io: Io, chans: R): Canceled!int
```

- Errors: Canceled

Waits until a channel of the record chans can Recv without waiting (a
value buffered, a sender waiting, or closed) and returns that field's
index, counted in byte order of the field names. The caller then calls
Recv on that field. chans is a record whose fields are each a Chan or
a &Chan.

The fields are read in that order, and the fields before the one found
are read again until none of them is ready. So a field made ready by a
Send or Close before the found field became ready is returned instead,
unless another task received from it meanwhile. The first ready field
wins, so a channel that is always ready starves the fields after it.
A Send wakes one Select watching its channel, and that Select passes
the wake on as it returns while the channel is still ready; a Close
wakes every one.

A cancellation point: Canceled in a cancelled scope, even when a
channel is ready, and Canceled from a cancelled wait. A record of more
than 64 fields is polled every 1 ms instead of waited on. O(n) per
wake for n channels, plus O(n) for each field before the found one that
a Send makes ready during the scan; O(n^2) at worst. It allocates
nothing: what it registers with each channel lives on its own stack.

### `Selectable`

```
export interface Selectable {
    Ready(&self): bool
    // std-internal methods: Watch, Unwatch and Pass
}
```

What Select can wait on. Chan[T] and &Chan[T] implement it for every
T, and no other type can: its methods name a type of std's own, which
no module outside std can import.

Ready reports whether Recv would not wait now: a value is buffered, a
sender is waiting, or the channel is closed. The answer may be stale as
soon as it is given; a Recv after a true Ready waits only if another
task received meanwhile. Watch, Unwatch and Pass are Select's own: they
register a waiting Select with the channel, take it off again, and pass
a wake on to one more waiting Select while the channel is still ready.

### `Semaphore`

```
export struct Semaphore
```

A count of permits that tasks take and give back, to bound how many do
something at once. A free permit goes to whichever task finds it, but a
waiter that has waited 1 ms and still finds too few starves: from then
on permits go to the waiters in arrival order, and nobody takes one past
them, so smaller requests never starve a large one. It holds at most
2^60 - 1 free permits. It allocates nothing, and each waiter waits in a
cell on its own task's stack.

### `Semaphore.Acquire`

```
export Semaphore.Acquire(&self, io: Io, n: int): Canceled!void
```

- Errors: Canceled

Waits until n permits are free, and takes them. A waiter that has
waited 1 ms is served before every later one. The task sees every write
made before the Release calls that gave its permits back.

A cancellation point first, then Canceled from a cancelled wait. A
cancelled Acquire holds no permit: those it was given while it waited
go to the next waiters. An n of 0 takes nothing; an n below 0 panics
with code 8. A request for more permits than will ever be released
waits until cancelled, and while it is first in the queue so does every
request queued behind it. It allocates nothing.

### `Semaphore.New`

```
export Semaphore.New(permits: int): Semaphore
```

A semaphore holding permits free permits. It allocates nothing, so it
can initialize a `var` global. A permits count below 0 or above
2^60 - 1 panics with code 8.

### `Semaphore.Release`

```
export Semaphore.Release(&self, n: int)
```

Gives n permits back. They go to the free count, waking a waiter to
try again, or, while a waiter starves, to the starving waiters first,
in arrival order. Takes no Io and is not a cancellation point. More
than 2^60 - 1 free permits panics with code 1 (the overflow panic); an
n below 0 panics with code 8. It allocates nothing.

### `Semaphore.TryAcquire`

```
export Semaphore.TryAcquire(&self, n: int): bool
```

Takes n permits and returns true if they are free and no waiter
starves; otherwise returns false and takes nothing. It never waits,
takes no Io and is not a cancellation point. An n of 0 is true; an n
below 0 panics with code 8. O(1); allocates nothing.

### `Shared`

```
export linear view struct Shared[T]
```

A handle on a value of type T in a reference-counted box on the heap,
for a value with no single owner, such as a cache entry or a
configuration many tasks read. Every handle reaches the same value,
read-only; put a Mutex or RwLock inside for a value that changes. The
box lives until the last handle is freed.

Linear: end each handle with Free. Send and Share when T is Send and
Share. A type may hold a Shared of itself, as
`Ring { next: Mutex[?sync.Shared[Ring]] }` does, with no layout cycle.
A cycle of Shared values is never freed: it leaks memory, and nothing
unsafe follows.

### `Shared.Clone`

```
export Shared[T].Clone(&self): Shared[T] from self.ptr
```

- Flows: The result borrows what self.ptr refers to; self is free after the call.

One more handle on the same box. The count's overflow panics with code
1, which takes far more handles than a program can make. The count's
change orders nothing: the new handle comes from a live one, which
already keeps the box alive. Not a cancellation point. O(1); allocates
nothing.

### `Shared.Free`

```
export Shared[T].Free(self, h: &Heap)
```

Ends this handle. The last handle drops the value and frees the box
into h, which must be the Heap New took it from; any other handle only
lowers the count. Each Free is a release of its handle's uses of the
value, and the last one an acquire of them all, so the task that frees
the box sees every other handle's use as done. A last Free of a box
holding a linear T panics with code 8, since the value would be dropped
unended. O(1) besides dropping the value; allocates nothing.

### `Shared.New`

```
export Shared[T].New(h: &Heap, x: T): OutOfMemory!Shared[T] from h, x
```

- Flows: The result borrows from h and x.
- Errors: OutOfMemory

A first handle on a new box holding x. x is consumed only on success:
OutOfMemory when h cannot give the box, and the caller keeps x. O(1);
allocates one box in h, an 8-byte count beside the value.

### `Shared.Ref`

```
export Shared[T].Ref(&self): &T
```

- Flows: The result borrows from self.

The shared value, for reading, borrowed from this handle. O(1);
allocates nothing.

<!-- api:end -->
