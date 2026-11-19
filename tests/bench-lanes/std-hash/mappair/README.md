# mappair (idiomatic table)

Task: the standard hash map of each language with its default hash, keys a struct of two `u64` fields, values `u64`. `mappair <n> <rounds> <seed>`; for each round `r` in `0..rounds`, with `s = seed + r` (wrapping):

1. A new, empty map.
2. Insert: `st = s`; for `i` in `0..n`: `st += 0x9e3779b97f4a7c15`, key `(mix(st), st)`, value `i`. `mix` is splitmix64's finalizer (`intsum`'s).
3. Probe: `st = s`; for `i` in `0..n`: `st += 0x9e3779b97f4a7c15`; `k = (mix(st), st)`; add the value found for `k`; add 1 if `(mix(st), st ^ 1)` is present (it never is at these sizes); if `i` is even, remove `k`.
4. Recheck: `st = s`; for `i` in `0..n`: add 3 if `(mix(st), st)` is present after the same step.
5. The round's value: the sum plus the map's length. All sums wrap.

The program prints the wrapping sum of the rounds' values. Checksums: quick size (`100000 3 1`) `15000450000`, publish size (`1000000 12 1`) `6000018000000`, the same from Mk, Rust, Zig and Go on an AMD Ryzen 7 9800X3D, Windows 11 (2026-11-06).

Each column uses its idiomatic form: Mk `Map[Pt, u64].New` in a sub-arena per round, `Pt` with the `Hash` impl a derive writes (each field's `Hash` in order); Rust `HashMap<(u64, u64), u64>::new()`; Zig `std.AutoHashMap(Pt, u64)` in an `ArenaAllocator` per round; Go `map[pt]uint64` with `pt` a struct of two `uint64`. C has no standard map, so its cell says so. Mk's key takes `HashOf`'s Hash impl path, the path every struct, enum and derived key takes, so this row prices it; Mk's `NewKeyed` is the peer of Rust's and Go's seeded hashes, `New` of Zig's fixed one.

The publish size comes from an informal hyperfine run on a loaded machine: the fastest cell (Zig ReleaseFast) took 0.93 s at 10 rounds, so the row runs 12, about 1.1 s projected; the 1 s floor is a projection until a `tools/bench publish` times the row.
