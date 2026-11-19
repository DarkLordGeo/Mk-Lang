# regexfindall (idiomatic table)

**Task.** `regexfindall <file> <rounds>`: compile `[a-z]+=5[0-9][0-9]` once; each round, find every non-overlapping match in the whole log (no line splitting) and add 1,000,000 plus each match's length to the checksum. The pattern has no literal prefix, so neither engine can skip to a fixed string: the row compares the automata on a match-dense text (`status=5xx` and `req=5xxxxx` both match).

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes, the file of `tests/bench/idiomatic/regex` (`tools/bench/gen.go` `genLog`).

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `Regex.FindAll` into an arena `Reset` per round: forward DFA to each match's end, reverse DFA back to its start |
| go | `regexp.MustCompile`, `FindAllStringIndex` |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `{data} 1`, publish `{data} 11`. Overflow class `never`. Checksums in bench.json, from `tools/bench verify` at both sizes. In one `tools/bench verify -publish-size` run at 10 rounds (2026-11-07, on a Ryzen 7 9800X3D shared with other work, not a published number) Mk took about 0.9 s and Go 12 to 17 s, so the Go cell dominates a publish.
