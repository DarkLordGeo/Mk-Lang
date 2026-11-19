# cancelpoint (idiomatic table)

Task: `cancelpoint <n> <expired>` takes and releases an uncontended mutex `n` times, with the library's cancellation check for the loop on each pass, and updates the guarded value as `x = x * 31 + i` (wrapping). It prints `n`, `x` and the failed calls (0).

- Variant `none` (`expired` 0): nothing anywhere is cancelled.
- Variant `expired` (`expired` 1): an unrelated scope is expired for the whole loop. In Mk, a sibling task opens a scope, cancels it with `Scope.CancelAfter` of 0, reports `Expired()`, and waits on a `NoCancel` `Io` until the loop ends. The loop's own scope chain holds no cancelled scope, so every `Lock` must succeed. Go's cell has another context expired; Rust's, Zig's and C's have another flag set.

The bar: Mk's `expired` cell stays within 1.25 of its `none` cell: an expired scope slows only the cancellation points under it.

Each column uses its idiomatic form:

| Column | Mutex | Cancellation check |
|---|---|---|
| Mk | `Mutex.Lock` | built into `Lock` (`CancelPoint`) |
| Go | `sync.Mutex` | `ctx.Err()` |
| Rust | `std::sync::Mutex` | an `AtomicBool` |
| Zig | `std.Io.Mutex` | built into `lock(io)` |
| C | `SRWLOCK` or `pthread_mutex_t` | an atomic `int` |

Checksums come from a Go model: `x = x * 31 + i` over `0..n`, wrapping.

| Size | Arguments | Checksum |
|---|---|---|
| quick | `1000000 e` | `1000000 9446225037035921696 0` |
| publish | `150000000 e` | `150000000 12940807836958910656 0` |
