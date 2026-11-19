# json-write (idiomatic table)

Workload: parse the 1,000,000-byte JSON document of `../parse` (`tools/bench gen` kind `json`, seed 2) once into the language's standard tree type, then serialise the tree as compact JSON into an in-memory buffer `reps` times. The parse is inside the program's time and runs once, against 300 writes at the publish size; `../parse` times the parse alone.

Checksum: the sum over reps of the output's byte length. The input's strings are printable ASCII with `\"` and `\` escapes only, so every writer escapes the same bytes; numbers are written as their text in every column, so the output length is the same whatever the member order.

| Column | Writer | Buffer |
|---|---|---|
| mk | `Value`'s `Format` (compact; `"`, `\` and controls escaped, nothing else) | one `Text`, `Clear` before each rep, written through `Text.Writer` |
| go | `encoding/json` `Encoder` with `SetEscapeHTML(false)`, of the `any` tree from a `Decoder` with `UseNumber` (map keys sorted) | one `bytes.Buffer`, `Reset` before each rep; the newline `Encode` adds is not counted |
| zig | `std.json.Stringify.valueAlloc` of the `std.json.Value` from `parseFromSliceLeaky` with `parse_numbers = false` (numbers as text) | one `ArenaAllocator`, `reset(.retain_capacity)` after each rep |
| c, rust | no std JSON | |
