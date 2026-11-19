# regexgroup (idiomatic table)

**Task.** `regexgroup <file> <rounds>`: compile `([a-z]{1,64})=([0-9]+)` once; each round, find every non-overlapping match in the whole log (no line splitting) and take group 1 of each, adding 1,000,000 plus the group's length. Every line has three matches (`req=`, `status=`, `dur=`). The counted repeat makes a program of about 130 instructions and the matches are a few bytes, so the row measures what a submatch costs on a short match of a mid-sized program, where clearing arrays sized by the program on every call would dominate.

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes, the file of `tests/bench/idiomatic/regex` (`tools/bench/gen.go` `genLog`).

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `Regex.FindAll` into an arena `Reset` per round, then `Regex.Group(text, m, 1)` per match: the bounded backtracker over the match's bytes (`std/regex/backtrack.mk`), the Pike VM when it gives up |
| go | `regexp.MustCompile`, `FindAllStringSubmatchIndex` (every group in one pass) |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `{data} 1`, publish `{data} 5`. Overflow class `never`. Checksums in bench.json: the quick one from both cells run by hand (2026-11-07, both printed 1597893391548), the publish one five times it; `tools/bench verify` checks both.
