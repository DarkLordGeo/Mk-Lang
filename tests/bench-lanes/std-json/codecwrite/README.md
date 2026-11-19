# json-codec-write (idiomatic table)

Workload: decode the 1,000,000-byte JSON document of `../parse` (`tools/bench gen` kind `json`, seed 2), an array of records `{"id","name","score","tags","ok","next"}`, into structs once, then write the structs back as compact JSON into an in-memory buffer `reps` times through each standard library's typed encoder. The decode is inside the program's time and runs once, against 300 writes at the publish size; `../codec` times the decode alone. The struct types are those of `../codec`: four levels `R0` to `R3`, `R3` without `next`. It is the row of the emitted `FormatJson`.

Checksum: `reps` times the count of the bytes `{` `}` `[` `]` `:` `,` and `"` in the last output. The float digits differ between the languages (Mk and Go write the shortest round-trip digits in their own exponent styles, Zig its own), so the output length does not agree; the structural bytes do, and they check that every encoder wrote the same records, keys, nesting, `null`s and strings (the names are printable ASCII with `\"` and `\` escapes, which every encoder writes as `\"` and `\\`).

| Column | Encoder | Buffer |
|---|---|---|
| mk | the `FormatJson` that `gen json.Codec(R0)` emits, per record, with `[`, `,` and `]` written by the program | one `Text`, `Clear` before each rep, written through `Text.Writer` (no buffer: one call per piece) |
| go | `encoding/json` `Encoder` with `SetEscapeHTML(false)` of `[]R0` (`*R1` for the optional) | one `bytes.Buffer`, `Reset` before each rep |
| zig | `std.json.Stringify.valueAlloc` of `[]R0` (inline `?R1`) | one `ArenaAllocator`, `reset(.retain_capacity)` after each rep |
| c, rust | no std JSON | |
