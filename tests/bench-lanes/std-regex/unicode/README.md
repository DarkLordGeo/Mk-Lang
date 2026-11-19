# regexunicode (idiomatic table)

**Task.** `regexunicode <file> <rounds>`: compile `\pL+` (a word of letters), `\w+\s\pL` and `\p{Lu}\pL*` (a capitalised word) once; each round, find every non-overlapping match of each in the whole log (no line splitting), adding 1,000,000 plus the length of every match. The three classes are large UTF-8 automata (`\pL` is 684 scalar ranges), so the row measures the matcher on Unicode tables: `\pL+` must keep both DFAs within the cell budget and `\pL` its reverse DFA within the work budget, or the search falls back to the Pike VM.

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes, the file of `tests/bench/idiomatic/regex` (`tools/bench/gen.go` `genLog`). ASCII only, so the row times the tables' ASCII part; the reverse class is checked on every scalar by `TestSTDREGEX_TestBlocks`.

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `Regex.FindAll` into an arena `Reset` after each pattern: forward DFA to each match's end, reverse DFA to its start |
| go | `regexp.MustCompile`, `FindAllStringIndex` |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `{data} 1`, publish `{data} 3`. Overflow class `never`. Checksums in bench.json: the quick one from both cells run by hand (2026-11-07, both printed 11185253115466), the publish one three times it, as every round adds the same; `tools/bench verify` checks both.
