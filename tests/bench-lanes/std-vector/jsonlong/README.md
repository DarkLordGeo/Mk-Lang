# jsonlong (idiomatic table)

Each language's std JSON over a document whose strings are long (100 to 1,099 bytes, an escape about every 256 bytes), where the width of the string scan shows (Mk: NextSpecial, 16-byte vectors, then an 8-byte SWAR word, before and after each escape); the rows of tests/bench-lanes/std-json use short strings (1 to 24 bytes, frequent escapes), where it does not.

- `parse`: parse the document reps times; the checksum is the sum of the decoded string lengths. Mk `json.Parse` into an arena Reset after each; Go `encoding/json` `Unmarshal` into `any`; Zig `std.json.parseFromSliceLeaky` into `std.json.Value`, an `ArenaAllocator` reset after each.
- `write`: one parse, then write the tree as compact JSON reps times; the checksum is the sum of the written lengths. Mk `Value.Format` into a Text; Go an `Encoder` with `SetEscapeHTML(false)` into a `bytes.Buffer` (its trailing newline not counted); Zig `std.json.Stringify.valueAlloc`.

The document comes from splitmix64(seed) inside each program, byte for byte the same (`Doc` in mk/jsonlong.mk, `doc` in testdata/jsonlong.go and jsonlong.zig), so no data file is needed. C and Rust have no std JSON. The row also serves `go run ./tools/bench compare --base <mk before> --head <mk after> -cols mk std-vector/jsonlong-parse std-vector/jsonlong-write`.
