# json-codec (idiomatic table)

Workload: decode the 1,000,000-byte JSON document of `../parse` (`tools/bench gen` kind `json`, seed 2), an array of records `{"id","name","score","tags","ok","next"}`, into structs, `reps` times, and sum the records. `next` is `null` or another record, at most three deep (`tools/bench/gen.go`), and a Mk struct cannot hold itself, so every column declares the same four levels `R0` to `R3`; `R3` has no `next`, so its `next` (always `null`) is an unknown member, ignored in every column.

Checksum (integers only, wrapping): over reps and over the records at every level, 1 + `id` + `name`'s decoded length + `score` truncated toward zero + the tag count + each tag's decoded length + 1 if `ok`. The scores have three decimals, so every correctly rounded parse truncates to the same integer.

| Column | Decoder | Records | Memory |
|---|---|---|---|
| mk | `std.json.Parse` into a `Value`, then `R0.FromJson` (from `gen json.Codec`) per element, pushed into a `List[R0]` | inline `?R1` | one `Arena`, `Reset` after each rep |
| go | `encoding/json` `Unmarshal` into `[]R0` | `*R1` (Go's optional) | the garbage collector |
| zig | `std.json.parseFromSliceLeaky([]R0, ...)`, `ignore_unknown_fields` | inline `?R1` | one `ArenaAllocator`, `reset(.retain_capacity)` after each rep |
| c, rust | no std JSON | | |

Mk decodes in two passes (the tree, then the structs), as Mk's codec is specified (`FromJson` reads a `Value`, doc/reference.md); Go and Zig decode the text straight into the structs. That is each standard library's idiomatic path, and the difference is inside the measured time.

UTF-8: every column checks the text's UTF-8 inside each rep. Mk's `Parse` takes a `str`, so the Mk column reads the bytes once and makes the `str` (`str.From`, one validating pass) inside each rep, before `Parse`: two passes where Go's and Zig's decoders check UTF-8 as they scan (validating once, outside the reps, would do less work per rep than its peers).
