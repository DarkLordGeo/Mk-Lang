# std-mem2 benchmark rows

Rows of `tools/bench` (idiomatic standard library table) for `std.mem`. Each row directory holds `bench.json`, the Mk package under `mk/`, and the Zig baseline. Only Zig has a standard fixed-buffer allocator (`std.heap.FixedBufferAllocator`); the C, Rust and Go cells say so in `skip`. `std.mem.Counting` and `std.mem.Null` have no standard-library peer in any of the four languages, so they have no row (Zig's `std.testing.allocator` tracks every allocation rather than counting, and `std.testing.FailingAllocator` is a test utility).

Inputs: splitmix64 (Steele, Lea and Flood 2014) from the seed in the arguments, the same in every column. Every program prints one checksum line.

## fixed

Per round: `st += 0x9e3779b97f4a7c15`, `r = mix(st)` (splitmix64's finaliser), `k = (r & 511) + 1`. Build a list of the `k` values `r +% i` (`i` in `0..k`) in the fixed-buffer allocator, with the language's standard growable list, appending one at a time. Sum the list with wrapping addition, add the sum to the total (wrapping), release the list, reset the allocator. Print the total. Usage: `fixed <rounds> <seed>`.

## fixed-alloc

Per iteration: `st += 0x9e3779b97f4a7c15`; allocate one `u64` holding `mix(st)`, then one holding `st`, through the standard allocator interface of the fixed-buffer allocator; `total +%= second`, free the second, `total ^= first`, free the first. Print the total. Usage: `fixedalloc <n> <seed>`.
