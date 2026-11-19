# textprops (idiomatic table)

One row with two variants under tests/bench/README.md's contract for the idiomatic table: a Unicode property lookup per scalar, each language through its standard library. Only `tools/bench` publishes a number from it.

## Task

Usage `textprops <variant> <reps> <seed>`. From `seed`, splitmix64 gives one word z per scalar, 65,536 scalars: range `k = z mod 6` of U+0020 to U+007E, U+00C0 to U+024F, U+0391 to U+03C9, U+0410 to U+044F, U+3041 to U+3096 and U+4E00 to U+9FA5, and the scalar `first + (z >> 8) mod count`. Each range was fully assigned with the same properties in every Unicode version since 5.0, so libraries built on different versions (Mk 18.0.0, Go 17.0.0, Rust's) agree. Then `reps` passes; each scalar with the property adds `1 + r` (r the pass) to a wrapping u64 sum, so the checksum for h hits a pass is `h * reps * (reps + 1) / 2`.

| Variant | Property |
|---|---|
| `alphabetic` | Alphabetic (DerivedCoreProperties.txt) |
| `upper` | General_Category Lu |

## Columns

- **Mk**: `text.Has(c, Property.Alphabetic)` and `text.CategoryOf(c) == Category.UppercaseLetter` (std/text/props.mk: one three-stage trie to a record, three loads and a field load).
- **Rust**: `char::is_alphabetic` (core's generated tables); no General_Category in std (upper skipped).
- **Go**: `unicode.In(c, unicode.Letter, unicode.Nl, unicode.Other_Alphabetic)`, Alphabetic's definition, and `unicode.IsUpper` (range tables, binary search above Latin-1).
- **C, Zig**: no Unicode property in std (skipped).

## Checksums

Built and run on 2026-11-18 (checksums only, no timing): at `10 1` alphabetic 3321725 (Mk, Go, Rust) and upper 1034825 (Mk, Go); at `100 1` 304994750 and 95015750. So h is 60,395 (alphabetic) and 18,815 (upper).
