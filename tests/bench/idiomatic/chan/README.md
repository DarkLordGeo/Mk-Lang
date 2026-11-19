# chan (idiomatic, concurrency)

**Task.** Channel ping-pong. `chan <n>`: two channels a and b of capacity 1; one side sends 0..n-1 on a and, after each send, receives the value back on b and adds it to the checksum; the other side receives on a and sends each value back on b. The checksum is `n * (n - 1) / 2`.

**Model.** Each cell names its model: Mk tasks with `Chan[i64]` in one `scope`, Go goroutines with `chan int64`, Rust two OS threads (`std::thread::scope`) with `std::sync::mpsc::sync_channel(1)`: Rust's std has threads, not tasks, so its cell is labelled threads. C and Zig have no std channel (C11 `threads.h` and Zig's `std.Thread` have mutexes and condition variables only), so their cells say so; no hand-written channel stands in. Every column runs on the same two logical CPUs, 0 and 2 (two physical cores; mask `0x5`), with two threads: `MK_CARRIERS=2`, `GOMAXPROCS=2` (bench.json `concurrency`). The ping-pong speed of a runtime depends on its thread count, so the count is stated and equal for all.

Sizes: quick `20000` (199990000), publish `4000000` (7999998000000; Go ran 2,000,000 in 0.62 s in a verify run on a shared machine, 2026-11-07, unpinned: verify sets the thread count but not the row's CPU set). Overflow class `never`.
