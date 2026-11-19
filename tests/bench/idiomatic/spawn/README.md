# spawn (idiomatic, concurrency)

**Task.** Start 1 M lightweight tasks and join them. `spawn <n> <rounds>`: per round, one scope (Mk) or one `sync.WaitGroup` (Go) starts n tasks, task i adds i to one atomic counter, and the round waits for all of them; the checksum is the sum of the counters over the rounds, `rounds * n * (n - 1) / 2`, which shows that every task ran exactly once.

**Model.** Only languages whose standard library has tasks have a cell: Mk's `scope` / `s.go` (M:N tasks on carriers) and Go's goroutines (M:N on GOMAXPROCS threads). C, Rust and Zig have OS threads only, and 1 M OS threads cannot be created on Windows, so their cells say "no std tasks"; no thread pool stands in. Both cells run on the same core set with the same thread count: logical CPUs 0 to 7 (affinity mask `0xff`), `MK_CARRIERS=8` and `GOMAXPROCS=8` (bench.json `concurrency`, set by tools/bench for this row only).

Sizes: quick `10000 1` (49995000), publish `1000000 8` (3999996000000; Go ran four rounds in 0.58 s in a verify run on a shared machine, 2026-11-07, unpinned: verify sets the thread count but not the row's CPU set). Overflow class `never`.
