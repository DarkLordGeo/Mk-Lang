# maphash (idiomatic table)

Task: the standard hash map of each language with its default hash, keys `u64`, values `u64`. `maphash <n> <rounds> <seed>`; for each round `r` in `0..rounds`, with `s = seed + r` (wrapping):

1. A new, empty map.
2. Insert: `st = s`; for `i` in `0..n`: `st += 0x9e3779b97f4a7c15`, key `mix(st)`, value `i`. `mix` is splitmix64's finalizer (`intsum`'s).
3. Probe: `st = s`; for `i` in `0..n`: `st += 0x9e3779b97f4a7c15`; `k = mix(st)`; add the value found for `k`; add 1 if `mix(st ^ 0x5555555555555555)` is present (it never is at these sizes); if `i` is even, remove `k`.
4. Recheck: `st = s`; for `i` in `0..n`: add 3 if `mix(st)` is present after the same step.
5. The round's value: the sum plus the map's length. All sums wrap.

The program prints the wrapping sum of the rounds' values. Checksums: quick size (`100000 3 1`) `15000450000`, publish size (`3000000 3 1`) `13500013500000`, the same from Mk, Rust, Zig and Go on an AMD Ryzen 7 9800X3D, Windows 11 (2026-11-06). The publish size is three times an earlier `1000000 3 1`: there the fastest cell (Zig) ran about 0.4 s in informal runs, under the contract's 1 s floor; three times n projects it to about 1.2 s, a projection until a `tools/bench publish` times the row.

Each column uses its idiomatic form: Mk `Map[u64, u64].New` in a sub-arena per round; Rust `HashMap::new()`; Zig `std.AutoHashMap` in an `ArenaAllocator` per round; Go `map[uint64]uint64`. C has no standard map, so its cell says so. Hash classes: Mk's `New` (the fold hash) and Zig's `AutoHashMap` (wyhash, seed 0) are fixed functions. Go's map is not: it draws a random seed for each map when the map is made, and Rust's `RandomState` keys SipHash-1-3 at random per process, so both resist chosen keys, and Mk's `NewKeyed` (SipHash-1-3 under a random key) is their peer in that respect. Go's hash is AES-based where the CPU has AES-NI, so it is fast as well as seeded.
