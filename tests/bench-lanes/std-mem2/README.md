# std-mem2 benchmark rows

Rows of `tools/bench` (idiomatic standard library table) for `std.mem`. Each row directory holds `bench.json`, the Mk package under `mk/`, and the baselines. Only Zig has a standard fixed-buffer allocator (`std.heap.FixedBufferAllocator`) and a standard allocator that serves from a buffer first and falls back to another (`std.heap.BufferFirstAllocator`); the C, Rust and Go cells of `fixed`, `fixed-alloc` and `fallback` say so in `skip`. None of the four has a standard allocator over a caller's buffer that frees and reuses any block, so `fixedheap-small` sets `std.mem.FixedHeap` beside each language's general-purpose allocator on one task. `std.mem.Counting`, `std.mem.Limit`, `std.mem.Failing` and `std.mem.Null` have no standard-library peer in any of the four languages, so they have no row (Zig's `std.testing.allocator` tracks every allocation rather than counting, and `std.testing.FailingAllocator` is a test utility).

Inputs: splitmix64 (Steele, Lea and Flood 2014) from the seed in the arguments, the same in every column. Every program prints one checksum line.

## fixed

Per round: `st += 0x9e3779b97f4a7c15`, `r = mix(st)` (splitmix64's finaliser), `k = (r & 511) + 1`. Build a list of the `k` values `r +% i` (`i` in `0..k`) in the fixed-buffer allocator, with the language's standard growable list, appending one at a time. Sum the list with wrapping addition, add the sum to the total (wrapping), release the list, reset the allocator. Print the total. Usage: `fixed <rounds> <seed>`.

## fixed-alloc

Per iteration: `st += 0x9e3779b97f4a7c15`; allocate one `u64` holding `mix(st)`, then one holding `st`, through the standard allocator interface of the fixed-buffer allocator; `total +%= second`, free the second, `total ^= first`, free the first. Print the total. Usage: `fixedalloc <n> <seed>`.

## fixedheap-small

The task of `tests/bench/idiomatic/alloc-small`, whose README states it in full: a ring of 4,096 live objects of 8 to 256 bytes, each step reading the oldest, allocating and filling a new one, then freeing the old. The new object is allocated before the old one is freed, and the sizes are spread over 249 values, so splitting, merging and reuse are what is priced. Usage: `fixedheap-small <steps> <seed>`.

| Column | Object | Allocator |
|---|---|---|
| mk | `HeapList[u8]` from empty, one `Extend` of the bytes; `Replace` into the ring, `Free` | `std.mem.FixedHeap` over a 2 MiB buffer taken once from the program's arena; the ring lives in it too |
| c | `malloc(l)` and `memcpy`; `free` | the static UCRT's `malloc` |
| rust | `Box<[u8]>` from `Box::from(&src[..])`; `std::mem::replace`, drop | the global allocator, `System` (HeapAlloc on Windows) |
| zig | `gpa.dupe(u8, ...)`; `gpa.free` | `std.heap.smp_allocator` |
| go | `bytes.Clone`; the slot is overwritten | Go's runtime allocator, garbage collected (GOGC=100, labelled: it frees in batches, not per step) |

The Mk cell of `alloc-small` runs the same program over std's general heap, so the two rows together compare `FixedHeap` with the `Heap`.

## fallback

Per round: `st += 0x9e3779b97f4a7c15`, `r = mix(st)`, `k = (r & 63) + 1`, or `k = (r & 1023) + 1` when `r >> 60 == 0` (one round in 16). Build a list of the `k` values `r +% i` (`i` in `0..k`) with the language's standard growable list, appending one at a time, in an allocator that serves from a 1 KiB stack buffer and falls back to the general-purpose allocator. Sum the list with wrapping addition, add the sum to the total (wrapping), release the list, make the buffer free again. Print the total. Usage: `fallback <rounds> <seed>`.

Most lists fit the buffer and never reach the general-purpose allocator; one in 16 outgrows it and moves there.
