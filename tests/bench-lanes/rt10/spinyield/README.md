# spinyield: the effect of Spin's yield

`mk/spinyield.mk` runs `t` tasks that each take one `atomic.Spin` `n` times; under the lock each spins `w` multiply-adds, so a holder that loses its CPU inside the section keeps every waiter spinning. Usage `spinyield <t> <n> <w>`; it prints `t n total`, `total = t * n` when every lock ran. `MK_CARRIERS` sets the carrier threads; above the CPU count holders are preempted, the case the yield is for.

There is no `bench.json`: this is an Mk-only A/B of one std change, not a cross-language row. The C, Rust and Zig columns of a spin-lock row (with `sched_yield` / `SwitchToThread` after the same 64 tries) are owed if the yield count is ever tuned.

## What changed

`std.core`'s `SpinLock` (the lock under `atomic.Spin` and std.sync's queue locks) counts every look at the lock word, each load of its wait loop and each failed `Cas`, as a try. From the 64th failed try on, each look first calls the yield hook `MkThreadYield`, and a failed `Cas` yields instead of the exponential pause backoff. The uncontended path is unchanged: the first `Cas` succeeds and returns before the counter is touched, so its instructions are the same as before. A contended look adds one increment and one compare.

In a release build LTO inlines the hook: the PDB of a `-release -debug` build shows `SpinLock` with the inlinee `MkThreadYield` (std.rt's) and under it `ThreadYield` (`SwitchToThread`), not the weak pause (`TestRt_SpinYieldHookLinked` checks dev and release).

## Measured (provisional)

Measured 2026-11-18 05:32 GST, x86_64-windows, 16 logical CPUs, release builds. **The PC was busy (other builds and tests running), not idle: every number is provisional.** `yield.exe` is std with the change; `noyield.exe` is the same std with `SpinYieldTries = 2^62` (never yields). Five runs each; wall-time median (min, max) and the median process CPU time (user + kernel, `GetProcessTimes`). The runner was a scratch script outside the repository.

| carriers | t | n | w | yield wall | noyield wall | yield CPU | noyield CPU |
|---|---|---|---|---|---|---|---|
| 1 | 1 | 50,000,000 | 0 | 0.271 s (0.265, 0.292) | 0.270 s (0.243, 0.303) | 0.234 s | 0.234 s |
| 16 | 16 | 2,000,000 | 20 | 0.311 s (0.231, 0.402) | 1.087 s (1.024, 1.141) | 0.84 s | 11.2 s |
| 32 | 32 | 1,000,000 | 20 | 0.256 s (0.240, 0.278) | 1.369 s (1.362, 1.551) | 0.66 s | 17.5 s |
| 64 | 64 | 500,000 | 20 | 0.249 s (0.227, 0.337) | 2.002 s (1.590, 2.266) | 0.80 s | 25.8 s |
| 64 | 64 | 200,000 | 200 | 0.318 s (0.299, 0.364) | 0.617 s (0.482, 0.913) | 0.55 s | 6.4 s |
| 128 | 128 | 250,000 | 20 | 0.282 s (0.248, 0.305) | 1.917 s (1.701, 2.375) | 0.81 s | 24.6 s |

No cost uncontended (about 5 ns a lock either way). Contended, at 16 to 128 carriers, the yielding lock ran 2 to 8 times faster in wall time and burned 10 to 30 times less CPU in these runs: a waiter that yields gives the holder its CPU back, and the lock passes along a short queue instead of being fought over by every spinner.

A version that counts only failed `Cas` calls as tries (04:47 to 04:51 GST, same machine, same load) never yields for a waiter behind a holder that keeps the lock; it gained a fifth at most and lost at 128 carriers.

An idle-machine run, and instruction and context-switch counts on Linux (`perf stat -e instructions,cycles,context-switches`), are owed before any number here is published.
