# json (idiomatic)

**Task.** Parse and serialise a 50 MB JSON document with the standard library, where one exists: Mk, Go and Zig. `json <file> <rounds>`; the file is read once; each round parses it into the std's tree, serialises the tree to compact JSON, parses that text again, and walks the second tree.

**Input.** `tools/bench gen` kind `json`, seed 4, 50,000,000 bytes (`tools/bench/gen.go` `genJSON`, the generator of `same/jsonscan`): an array of records with integer ids, ASCII names with `\"` and `\` escapes, scores with three decimals, string tags, booleans and nested records.

**Checksum.** The serialised bytes differ by library (Go sorts map keys and escapes `<`, `>` and `&`; Zig writes its numbers as it parsed them, `i64` or `f64`; Mk keeps member order and each number's text), so the checksum is taken over the reparsed tree, independent of member order: per value, `1 + trunc(number)` for a number, `1 + decoded length` for a string, 3 for true, 5 for false, 7 for null, 1 for an array or object; per object member, `1 + key length` more; all summed with wrapping `i64` arithmetic. Every column printed the same line at the quick size, 2026-11-07.

**Cells.**

| Column | Parse | Serialise |
|---|---|---|
| mk | `json.Parse` into a sub-arena per round (numbers as text, strings as views unless escaped) | `Value.Format` into a `Text` through its `Writer` |
| zig-fast, zig-safe | `std.json.parseFromSliceLeaky(std.json.Value)` into an arena reset per round (numbers as `i64` or `f64`) | `std.json.Stringify.valueAlloc` |
| go | `json.Decoder` with `UseNumber` into `any` | `json.Marshal` |
| c | no std JSON in C | |
| rust | no std JSON in Rust (`serde_json` is a crate; no crate stands in) | |

Sizes: quick `{data} 1` (207059989197776), publish `{data} 3` (three times that). Overflow class `wraps`.
