# mutex (idiomatic, concurrency: a lock under contention and without it)

**Task.** t workers each take one shared lock n times and add 1 under it, with each language's standard mutex. This row prices `Mutex`; it is a concurrency row. `mutex <n> <t>`:

```
m = a mutex guarding count = 0
start t workers, each:
    repeat n times: lock m; count = count + 1; unlock m
wait for all
print count                                   (t * n)
```

Two variants: `mutex-contended`, 8 workers on 8 threads, where the lock's contended path (spinning, parking and waking) is what is priced; and `mutex-uncontended`, 1 worker, the lock's fast path alone.

**Concurrency.** The row is not pinned to one CPU: every program inherits mask `0x5555` (8 logical CPUs, one per physical core) and 8 threads (`GOMAXPROCS=8`, `MK_CARRIERS=8`), as `std-sync2/atomicadd`. Each cell's model is labelled:

| Column | Workers | Lock |
|---|---|---|
| mk | tasks of one `Scope` (M:N on 8 carriers) | `Mutex[int]`: `Lock(io)`, `Guard.Mut`, `Unlock`; the count taken back with `Into` |
| c | OS threads, `<threads.h>` `thrd_create` | `mtx_t` (`mtx_plain`): `mtx_lock`, `mtx_unlock` |
| rust, rust-ovf | OS threads, `std::thread::scope` | `std::sync::Mutex<i64>`: `lock()`, the guard dropped |
| zig-fast, zig-safe | OS threads, `std.Thread.spawn` | `std.Io.Mutex`: `lockUncancelable(io)`, `unlock(io)` |
| go | goroutines (`GOMAXPROCS=8`), `sync.WaitGroup.Go` | `sync.Mutex`: `Lock`, `Unlock` |

Mk's `Lock` is cancellable (it returns `Canceled` in a cancelled scope); nothing cancels here, so the worker's `else` never runs. Zig's `lock` is cancellable too, and the Zig cell calls the uncancelable form, as `std-sync2/notify` does.

Sizes: contended quick `20000 8`, publish `2000000 8`; uncontended quick `100000 1`, publish `50000000 1`. The publish sizes are not yet checked against the 1 s floor: `tools/bench verify -publish-size mutex-contended mutex-uncontended` sets them. Overflow class `never` (the count stays below 2^31).
