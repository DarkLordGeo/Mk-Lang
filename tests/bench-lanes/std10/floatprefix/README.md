# floatprefix (idiomatic table)

One row under tests/bench/README.md's contract for the idiomatic table: parse the float at the start of a text and say where it ends, as ECMA-262's parseFloat needs. Only `tools/bench` publishes a number from it.

## Task

Usage `floatprefix <reps> <seed>`. From `seed`, splitmix64 gives one word z per text, 4,096 texts: `<a>.<b>e<e>;` with `a = z mod 1000000`, `b = (z >> 20) mod 10000000` and `e = (z >> 50) mod 41 - 20`, all written in decimal with no padding. Then `reps` passes, pass r starting at text r, each parse folded into a wrapping u64 sum: `bits >> 32` of the value plus the bytes used (the `;` is never used). The values are correctly rounded in every column, so the sums agree. Every pass adds the same sum.

## Columns

- **Mk**: `str.ParseFloatPrefix` (std/core/core_parse.mk: one scan for the prefix's end, then ParseFloat's Clinger fast path, unrounded scaling and multiprecision fallback on the prefix).
- **C**: `strtod` with its end pointer.
- **Rust, Zig, Go**: skipped: their std parses a whole string only, so finding the float's end, which is the task, would be the caller's code (bench.json).

## Checksums

Built and run on 2026-11-18 (checksums only, no timing): Mk and C give 447279323753900 at `100 1` and 4472793237539000 at `1000 1`.
