# std-missing benchmark rows

Rows of `tools/bench` (idiomatic standard library table) for `math.Rng`, `sync.RwLock`, `atomic.Spin` and `sync.Select`. Each row directory holds `bench.json` and the Mk package under `mk/`.

## rng

`math.Rng` (xoshiro256** seeded through SplitMix64). `rng <n> <seed>`: `r = Rng.New(seed)`; n times, `total +%= r.U64()`, then `total +%= r.Below(1000)`; print `total`. No other language's standard library has xoshiro256**: Zig's `std.Random.Xoshiro256` is xoshiro256++, Go's `math/rand/v2` has PCG and ChaCha8, and C and Rust have no comparable standard generator, so the row has the Mk column only until `tools/bench` accepts a checksum per column.

## rwlock

`sync.RwLock` (writer-preferring, after Go's `sync.RWMutex`). `rwlock <n> <t>`: t workers share one pair `{a, b}`; each does n operations: when `i % 10 == 0` it takes the write lock and adds one to `a` and to `b`, otherwise it takes the read lock and counts a pair with `a != b`. Prints `a` (t times ceil(n / 10)) and the mismatches (0). Columns: Mk tasks on 8 carriers, Go goroutines with `sync.RWMutex`, Rust `std::sync::RwLock` and Zig `std.Io.RwLock` on OS threads (labelled threads). C has no reader-writer lock in its standard library. A concurrency row: mask 0x5555, 8 threads.

## spin

`atomic.Spin` (test-and-test-and-set with bounded exponential backoff, Anderson 1990; layer 0, no Io). `spin <n> <t>`: t workers each take the lock n times and add one to a shared counter; prints the counter (t times n). Only C has a spin lock in its standard library (a C11 `atomic_flag`, test-and-set); Go `sync.Mutex`, Rust `std::sync::Mutex` and Zig `std.Io.Mutex` stand in as each std's best facility for a short critical section, and the row says so. A concurrency row: mask 0x5555, 8 threads.

## xoshiro

The same-algorithm row (table `same`) for `Rng.U64` and `Rng.Below`: xoshiro256** (Blackman and Vigna, `xoshiro256starstar.c`) seeded with four SplitMix64 outputs, and Lemire's nearly divisionless bounded draw. `xoshiro <n> <seed>`: n rounds of `total +%= next()`, then `total +%= below(1000)`; print `total`. The workload and the checksums are the `rng` row's. The Mk column is `std.math.Rng`, which is this algorithm, so it measures std's code as Mk compiles it (`raw.RotL64`, `raw.Mul128`, the wrapping operators); C uses `unsigned __int128`, Rust `u128`, Zig a `u128` product, Go `bits.Mul64`. Every column wraps where the algorithm does, so the checked builds (rust-ovf, zig-safe) have nothing to trap on.

## selidle

What a parked `sync.Select` costs channel traffic that has nothing to do with it. `selidle <n> <k>`: k tasks park in a select over two channels (`idle`, `quit`) that nobody uses; after 20 ms (both columns), one producer sends 0..n-1 on a channel of capacity 64 and closes it, and one consumer sums what it receives (most Sends buffer, the path that notifies Selects); then `quit` is closed, which releases the k selects. Prints the sum plus k. Mk and Go only: no other std has a select over channels. The published row runs k = 1; run it by hand with k = 0 and k = 1,000 for the curve.
