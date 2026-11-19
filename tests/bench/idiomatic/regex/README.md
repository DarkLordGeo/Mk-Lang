# regex (idiomatic)

**Task.** A log filter with the standard regex library, where one exists: Mk and Go. `regex <file> <rounds>`: the pattern `status=5[0-9][0-9] dur=[0-9]{4,}ms` (server errors that took at least a second) is compiled once; each round splits the log into lines with the std splitter and runs a find on every line; each match adds 1,000,000 plus its length to the checksum.

**Input.** `tools/bench gen` kind `log`, seed 5, 50,000,000 bytes (`tools/bench/gen.go` `genLog`): lines like `2026-11-07T03:14:15.926Z INFO svc=api req=123456 path=/items/abc/42 status=200 dur=17ms`, every field drawn from splitmix64; three of the twelve status codes are 5xx and about half of the durations have four digits.

**Cells.**

| Column | Engine | Per line |
|---|---|---|
| mk | `regex.Compile` (RE2 syntax, Pike VM, linear time) | `str.Lines`, `Regex.Find` |
| go | `regexp.MustCompile` (RE2 syntax; it scans for the literal prefix `status=5` before running the automaton) | `strings.Lines`, `FindStringIndex` |
| c | no std regex in C (POSIX `regex.h` is not ISO C, and the MSVC CRT has none) | |
| rust | no std regex in Rust (the `regex` crate is not std; no crate stands in) | |
| zig | no std regex in Zig | |

Sizes: quick `{data} 1` (66754401813: 66,754 matching lines), publish `{data} 22` (22 times that; the fastest cell, Go, ran one round in 64 ms, and 20 rounds in 0.97 s, in a verify run on a shared machine, 2026-11-07, and Mk in 767 ms). Overflow class `never`.
