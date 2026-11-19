# regexpatho (idiomatic table)

**Task.** `regexpatho <n> <rounds>`: compile `(a|aa)*c` once; each round, find it in `n` bytes of `a` (no match: a backtracking engine takes time exponential in `n` here) and in the same bytes followed by `c` (one match, the whole text). The checksum adds `n` plus the second match's length per round, and subtracts anything the first search finds. The inputs are built in the program.

**Cells.**

| Column | Engine |
|---|---|
| mk | `regex.Compile`, `Regex.Find` (forward, then reverse DFA) |
| go | `regexp.MustCompile`, `FindStringIndex` (its NFA simulation; the input is past its bounded backtracker's limit) |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) |
| zig | no std regex in Zig |

Sizes: quick `100000 2` (400002), publish `10000000 10` (200000010). Overflow class `never`.
