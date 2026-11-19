# ws-extend (idiomatic table)

Task: `ws-extend <n> <piece> <rounds>`. The input is `n` bytes of splitmix64 output, as in `ws-mask`. Per round `r` in `0..rounds`: a new, empty growable byte list; append the `n` bytes in order, piece `j` (from 0) of `1 + (r + j) % piece` bytes, the last cut at `n`; add to a wrapping `u64` sum the list's byte at `(r * 7919) % n` and the number of pieces; free the list. The program prints the sum.

Checksums (from the Go column): quick (`65537 100 100`) `142327`, publish (`65536 4096 100000`) `18695310`.

Columns, one job: a bulk append of a slice to each language's standard growable byte list, growth and allocator included, so the row compares libraries, not code generators (the idiomatic table of tests/bench/README.md). It is not a same-algorithm row: its columns share no data structure, growth policy or allocator.
- Mk: std.core's `HeapList[u8].Extend`: one `Grow` to fit (capacity doubled, at least 4), then one copy loop; on `sys.heap`'s allocator, freed each round.
- C: no cell. C's standard library has `realloc` and `memcpy` but no growable array type, so a C cell would time hand-written code.
- Rust: `Vec<u8>::extend_from_slice` (doubling, at least 8), on the global allocator, dropped each round.
- Zig: `std.ArrayList(u8).appendSlice` on `std.heap.smp_allocator` (about 1.5 times plus a cache line), deinit each round.
- Go: `append(v, data[at:at+k]...)` on a nil slice (doubling below 256 elements, about 1.25 times above), on the garbage-collected heap: dropped each round and reclaimed by the collector.

This is the append std.websocket's `Recv` makes for each read-ahead's worth of a fragmented message (`ReadHalf.Gather`), one `Extend` per piece instead of one `Push` per byte. Pieces average about half of `piece`; 4,096 keeps them near a read-ahead's worth.