# wordfreq (idiomatic)

**Task.** Count the word frequencies of a 50 MB text with the standard hash map. Each language reads the file once, then for each of `rounds` rounds: splits the text into words at whitespace with its std splitter, counts every word in a fresh std hash map that grows from empty, the keys borrowed from the text (no copy per word), and folds the entries into the checksum.

**Input.** `tools/bench gen` kind `words`, seed 3, 50,000,000 bytes (`tools/bench/gen.go` `genWords`): a vocabulary of 65,536 words of 1 to 12 lowercase ASCII letters, then words drawn with a skew toward low ranks (`vocab[(a % V) * (b % V) / V]`), separated by a space, or a newline one time in twelve. All ASCII, so every language's whitespace splitter (Unicode or ASCII) cuts the same words.

**Checksum.** Per round, over the map's entries in whatever order the map gives: the wrapping sum of `fnv1a64(word) * count`, plus the number of distinct words; the rounds' results summed, wrapping. It does not depend on the iteration order, and it changes if any word is counted wrong. Overflow class `wraps`.

**Cells** (each names its hash and its DoS posture):

| Column | Splitter | Map | Hash | DoS posture |
|---|---|---|---|---|
| mk | `str.Words` | `Map[str, u64].New`, `Ensure` | the fast fixed hash | none: a public fixed key |
| mk-keyed | `str.Words` | `Map[str, u64].NewKeyed(HashKey.New(io))` | SipHash-1-3, random per process | keyed (Rust's peer) |
| rust, rust-ovf | `str::split_whitespace` | `HashMap<&str, u64>`, `entry().or_insert(0)` | SipHash-1-3 under `RandomState` | keyed |
| zig-fast, zig-safe | `std.mem.tokenizeAny` | `std.StringHashMap(u64)`, `getOrPut`, an arena per round | Wyhash, seed 0 | none: fixed |
| go | `strings.FieldsSeq` | `map[string]uint64`, `m[w]++` | runtime AES hash, seeded per map | seeded |
| c | | no std hash map in C | | |

Sizes: quick `{data} 1`, publish `{data} 10` (the fastest cell, Zig ReleaseFast, ran one round in 129 ms in a verify run on a shared machine, 2026-11-07, so ten rounds clear the 1 s floor).
