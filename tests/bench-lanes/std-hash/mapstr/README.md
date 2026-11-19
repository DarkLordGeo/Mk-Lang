# mapstr (idiomatic table)

Task: the standard hash map of each language with its default string hash, keys borrowed strings, values `u64`. `mapstr <n> <rounds> <seed> <minlen> <maxlen>`. The same programs serve the row `mapstrlong` (keys of 24 to 200 bytes), which has its own `bench.json`.

Keys, generated once before the rounds: `st = seed`; for `i` in `0..n`: `st += 0x9e3779b97f4a7c15` (wrapping); `z = mix(st)` (`mix` is splitmix64's finalizer, `intsum`'s); the key's length is `minlen + z % (maxlen - minlen + 1)`; for byte `j` of the key, first `z = mix(z + j)` when `j % 8 == 0`, then the byte is `'a' + ((z >> (8 * (j % 8))) & 0xFF) % 26`. Key `i`'s miss is the same bytes with the first letter in upper case, which no key has. Every column keeps the keys as views of one buffer (Mk `str` from one array split by `Words`, Rust `&str`, Zig `[]const u8`, Go substrings), so no key is copied.

Each round `r` in `0..rounds`:

1. A new, empty map.
2. Insert key `i` with value `i + r` for `i` in `0..n` (a repeated key keeps the last value).
3. Probe: for `i` in `0..n`: add the value found for key `i`; add 1 if miss `i` is present (it never is); if `i` is even, remove key `i`.
4. Recheck: add 3 for each key `i` still present.
5. The round's value: the sum plus the map's length. All sums wrap.

The program prints the wrapping sum of the rounds' values. Checksums (Mk, Rust, Zig and Go agree, AMD Ryzen 7 9800X3D, Windows 11, 2026-11-06):

| Row | Quick | Publish |
|---|---|---|
| `mapstr` (1 to 7 bytes) | `100000 3 1 1 7`: `11067391680` | `1000000 10 1 1 7`: `3173286910710` |
| `mapstrlong` (24 to 200 bytes) | `30000 3 1 24 200`: `1350225000` | `300000 20 1 24 200`: `900066000000` |

Each column uses its idiomatic form: Mk `Map[str, u64].New` in a sub-arena per round; Rust `HashMap<&str, u64>::new()`; Zig `std.StringHashMap(u64)` in an `ArenaAllocator` per round; Go `map[string]uint64`. C has no standard map, so its cell says so. Hash classes: Mk's `New` (the fold hash) and Zig's `StringHashMap` (Wyhash, seed 0) are fixed functions; Go seeds its hash per map and Rust's `RandomState` per process, so they resist chosen keys and Mk's `NewKeyed` is their peer.

The publish sizes come from informal hyperfine runs on a loaded machine: `mapstr`'s fastest cell (Zig ReleaseFast) took 1.08 s at `1000000 10 1 1 7`; `mapstrlong`'s (Go) 0.59 s at 10 rounds, so it runs 20. The 1 s floor is a projection until a `tools/bench publish` times the rows.
