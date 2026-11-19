# timers (idiomatic, concurrency: sleeping tasks and deadlines at scale)

**Task.** Two uses of a runtime's timers (task sleep and `Scope.CancelAfter`; a concurrency row).

`timers-sleep`, `timers sleep <n> <rounds> <seed>`:

```
st = seed; total = 0
for r in 0 .. rounds-1:
    sum = an atomic counter at 0
    start n tasks; before task i starts: st = st + G; ms = mix(st) mod 50 + 1
        task i: t0 = now; sleep ms milliseconds
                if now - t0 >= ms / 2: sum = sum + i     (a sleep that does not wait adds nothing)
    wait for all
    total = total + sum                       (rounds * n(n-1)/2)
```

`timers-deadline`, `timers deadline <n>`:

```
disarmed = 0; fired = 0
for i in 0 .. n-1:
    d = 0 s if i mod 1000 = 999, else 60 s
    open a cancellation scope with deadline d; leave it at once
        (a 60 s deadline is disarmed unfired; a 0 s one has expired already)
    fired = fired + 1 if it expired, else disarmed = disarmed + 1
print disarmed + 3 * fired                    (n + 2 * (n / 1000) for n a multiple of 1000)
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). The sleep variant holds n timers at once, so it prices the timer queue's insert and expiry at scale plus the wake of each task, beside about 50 ms of waiting per round that every column shares. That shared floor (about 0.5 s at the publish size of 10 rounds) is in every column's time, so it pulls the variant's ratios towards 1. The deadline variant prices arming and disarming one timer, almost never fired.

**What the checksums show.** Each sleeper reads the clock around its sleep (Mk `time.Now` and `Instant.Since`, Go `time.Now` and `time.Since`) and adds its index only if it slept at least half its time, so a sleep that returns at once gives a smaller sum. Half, not all, so that a timer that wakes slightly early on a coarse clock does not change the checksum; the reads cost two clock calls per task in both columns. In the deadline variant every 1000th scope gets a 0 s deadline, which has expired before the scope is left (Mk: "a zero or negative `d` cancels at once", doc/reference.md, Deadlines, and `Expired()` is true; Go: `WithTimeout` with 0 returns a context whose `Err` is `DeadlineExceeded`), so a deadline call that does nothing gives n instead of n + 2n/1000. A 0 s deadline takes each std's at-once path (Mk's `NativeDeadline` in std/task/deadline.mk, Go's `WithDeadlineCause` in src/context/context.go), which arms no timer. So the checksum does not show that a positive deadline is armed or disarmed: a cell that ignored every positive deadline would print the same checksum (a check on 2026-11-15 built that mutant of each cell, and both printed 100200 at the quick size). That the 60 s ones were armed and then disarmed is not observable without waiting for them.

**Concurrency.** Mask `0xff`, 8 threads (`GOMAXPROCS=8`, `MK_CARRIERS=8`), as `spawn`. Only languages whose std has lightweight tasks and timers on them have a cell:

| Column | Sleep | Deadline |
|---|---|---|
| mk | tasks of one `Scope`, `Io.Sleep(time.Millis(ms))` | `scope s := io.Scope() { s.CancelAfter(d) }`, `d` = `time.Seconds(60)`, or `time.Seconds(0)` on every 1000th; `Expired` |
| go | goroutines, `time.Sleep` | `context.WithTimeout(d)` and its `cancel` (Go's peer of a scope deadline), `d` = 60 s, or 0 on every 1000th; `Err` |
| c, rust | none: OS threads only, and 10^5 of them cannot be created on Windows (as `spawn`); no cancellation scope with a deadline | |
| zig | none: `main`'s `Io` is `std.Io.Threaded` (lib/std/start.zig). `Io.concurrent` takes a pool thread per busy unit (`concurrent_limit` defaults to unlimited), and `Io.Group`'s `async` is bounded by `async_limit` (one less than the logical CPUs) and past it runs the unit at once on the caller (lib/std/Io/Threaded.zig), so 10^5 concurrent sleepers are not possible; `std.Io.Timeout` is a deadline on one operation, not a scope's | |

Sizes: sleep quick `sleep 10000 1 1`, publish `sleep 100000 10 1`; deadline quick `deadline 100000` (checksum 100200), publish `deadline 5000000` (checksum 5010000). The publish sizes are not yet checked against the 1 s floor: `tools/bench verify -publish-size timers-sleep timers-deadline` on a quiet machine sets them. Overflow class `never`.
