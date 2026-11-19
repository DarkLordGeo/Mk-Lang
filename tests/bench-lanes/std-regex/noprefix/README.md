# regexnoprefix (idiomatic table)

**Task.** `regexnoprefix <file> <rounds>`: the log filter of `tests/bench/idiomatic/regex` with its literal prefix taken out, `[a-z]+=5[0-9]{2} [a-z]+=[0-9]{4,}ms`, compiled once; each round splits the log into lines with the std splitter and runs a find on every line, adding 1,000,000 plus the match's length. `idiomatic/regex` lets Go skip to `status=5` with a substring search; this row does not, so it compares the automata.

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes, the file of `tests/bench/idiomatic/regex` (`tools/bench/gen.go` `genLog`).

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `str.Lines`, `Regex.Find` (forward, then reverse DFA) |
| go | `regexp.MustCompile`, `strings.Lines` with each line's `\n` or `\r\n` cut off, as `str.Lines` cuts it, so both engines read the same bytes, `FindStringIndex` |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `{data} 1`, publish `{data} 11`. Overflow class `never`. Checksums in bench.json, from `tools/bench verify` at both sizes. In one `tools/bench verify -publish-size` run at 10 rounds (2026-11-07, on a Ryzen 7 9800X3D shared with other work, not a published number) Mk took about 0.9 s and Go 12 to 17 s, so the Go cell dominates a publish.
