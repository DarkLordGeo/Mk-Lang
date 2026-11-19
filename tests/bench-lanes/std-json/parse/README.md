# json-parse (idiomatic table)

Workload: parse a 1,000,000-byte JSON document into the language's standard tree type, `reps` times, and walk every tree. The document is `tools/bench gen` kind `json`, seed 2: one array of records `{"id":<int>,"name":"<ascii>","score":<int>.<3 digits>,"tags":[...],"ok":<bool>,"next":null|{...}}` (`tools/bench/gen.go`), printable ASCII strings with `\"` and `\` escapes, no duplicate keys.

Checksum: the sum over reps of the tree's walk, where a value counts 1, a string adds its decoded byte length, and an object member adds its key's decoded byte length. Every parser must read the same tree to agree.

| Column | Parser | Tree | Memory |
|---|---|---|---|
| mk | `std.json.Parse` | `json.Value`, strings are views unless escaped, numbers as text | one `Arena`, `Reset` after each parse |
| go | `encoding/json` `Decoder` with `UseNumber` | `any` (maps, slices, `json.Number`) | the garbage collector |
| zig | `std.json.parseFromSliceLeaky(std.json.Value, ..., .{ .parse_numbers = false })` | `std.json.Value`, numbers as text (`.number_string`), as in Mk and Go (no conversion of every number to `i64`/`f64`) | one `ArenaAllocator`, `reset(.retain_capacity)` after each parse |
| c, rust | no std JSON | | |

UTF-8: every column checks the text's UTF-8 inside each rep. Mk's `Parse` takes a `str`, so the Mk column reads the bytes once and makes the `str` (`str.From`, one validating pass) inside each rep, before `Parse`: two passes where Go's and Zig's decoders check UTF-8 as they scan (validating once, outside the reps, would do less work per rep than its peers).
