# regexreplace (idiomatic table)

**Task.** `regexreplace <file> <rounds>`: compile `(svc|req)=([a-z0-9]+)` once; each round, replace every non-overlapping match in the whole log (no line splitting) by `$2:$1`, and add the output's length and every 4,096th output byte, weighted by `i / 4096 % 251 + 1`, to the checksum. Every line has two matches, so the row measures the search plus one capture pass per match: the template names two groups.

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes, the file of `tests/bench/idiomatic/regex` (`tools/bench/gen.go` `genLog`).

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `Regex.Replace` into an arena `Reset` per round: forward and reverse DFA to each match, then one Pike VM pass with every capture slot anchored at the match's start (`std/regex/capture.mk`) |
| go | `regexp.MustCompile`, `ReplaceAllString` |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `{data} 1`, publish `{data} 3`. Overflow class `never`. Checksums in bench.json, from `tools/bench verify` at both sizes. In one `tools/bench verify -publish-size` run (2026-11-07, on a Ryzen 7 9800X3D shared with other work, not a published number) Mk took 2.125 s and Go 4.808 s.
