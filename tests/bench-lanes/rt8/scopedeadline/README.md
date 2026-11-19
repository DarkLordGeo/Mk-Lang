# scopedeadline (idiomatic table)

Task: `scopedeadline <n> <t>` runs `n` rounds of the library's per-operation deadline of one second, split evenly over `t` tasks or goroutines. The deadline never fires. Task `k` runs rounds `i = k m .. k m + m - 1`, where `m = n / t`, and updates `x = x * 31 + i` (wrapping). The program prints `n`, `t`, the wrapping sum of the tasks' `x`, and the rounds that expired (0).

- Variants `t1`, `t2`, `t4`, `t8` and `t16` set `t` to 1, 2, 4, 8 and 16.
- Every variant runs on 16 threads (`concurrency`: `MK_CARRIERS` and `GOMAXPROCS` 16), so the variants differ only in how many tasks arm deadlines at once.

The bar: Mk's aggregate throughput at `t16` is at least its throughput at `t1`. Every variant does the same `n` rounds, so the bar is that `t16`'s time is at most `t1`'s, on both targets. Each carrier has its own timer heap and lock. With one heap behind one lock for the whole process, a hand measurement gave 75 ns per scope at one task and 157 to 173 ns at 16 (not a tools/bench number). No number for this row is recorded yet: the bar is owed to a tools/bench publish on a quiet machine.

Each column uses its idiomatic form:

| Column | Deadline form | Cost per round |
|---|---|---|
| Mk | a scope with `Scope.CancelAfter(1 s)`; the scope's exit (Join) disarms it | one insert and one remove on the running carrier's timer heap |
| Go | `context.WithTimeout(ctx, 1 s)`, `ctx.Err()`, `cancel()` | a context allocation, and one timer start and stop on the P's timer heap |
| C, Rust, Zig | not written (bench.json's `skip`) | |

Checksums come from a Go model: `x = 31 x + i` over each task's rounds, wrapping, summed over the tasks.

| Size | Arguments | Checksum |
|---|---|---|
| quick | `16000 1` | `16000 1 12723968583238000448 0` |
| quick | `16000 2` | `16000 2 5013796487217979200 0` |
| quick | `16000 4` | `16000 4 4964674106466705216 0` |
| quick | `16000 8` | `16000 8 7095224778594541376 0` |
| quick | `16000 16` | `16000 16 2314309535196544832 0` |
| publish | `16000000 1` | `16000000 1 12840768697313661440 0` |
| publish | `16000000 2` | `16000000 2 1204445165592646144 0` |
| publish | `16000000 4` | `16000000 4 12841475749935714816 0` |
| publish | `16000000 8` | `16000000 8 8958897959798706688 0` |
| publish | `16000000 16` | `16000000 16 8167049306680726016 0` |
