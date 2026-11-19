# json-small (idiomatic, many small JSON documents)

**Task.** The `json` row's work on small documents, one at a time, as an API server parses requests: each line of the input is one document, parsed into the std's tree, serialised to compact JSON, parsed again and walked. `json-small <file> <rounds>`; the file is the `json` row's (`tools/bench gen` kind `json`, seed 4, 50,000,000 bytes, the same spec, so one generated file serves both rows), read once.

**Input.** The generator writes one JSON array with one record per line: `[` before the first, `,` after each but the last, `]` after the last, and no newline inside a record (strings are printable ASCII). So each line, with a leading `[` and a trailing `,` or `]` cut off, is one complete document (a record and up to three nested records): the file has 312,017 lines of 75 to 661 bytes, 159 on average (counted by `awk` over the generated file, 2026-11-15), smaller than documents of about 1 KiB. No generator change was needed (no small-object option in tools/bench/gen.go).

```
total = 0
for r in 0 .. rounds-1:
    for line in lines of the file:
        d = line without a leading "[" and without a trailing "," or "]"
        if d is empty: next
        v = std parse of d; s = std compact serialisation of v; w = std parse of s
        total = total + walk(w)                (wrapping i64)
```

`walk` is the `json` row's order-independent checksum: per value, `1 + trunc(number)` for a number, `1 + decoded length` for a string, 3 for true, 5 for false, 7 for null, 1 for an array or object; per object member, `1 + key length` more.

**Cells.** Each column frees a document's trees before the next, as the library makes natural:

| Column | Parse | Serialise | Memory per document |
|---|---|---|---|
| mk | `json.Parse` | `Value.Format` into a `Text` through its `Writer` | a sub-arena per round, `Reset` after each document |
| zig-fast, zig-safe | `std.json.parseFromSliceLeaky(std.json.Value)` | `std.json.Stringify.valueAlloc` | an `ArenaAllocator` over `page_allocator`, `reset(.retain_capacity)` after each document |
| go | `json.Decoder` with `UseNumber` into `any` | `json.Marshal` | garbage collected |
| c | none: no std JSON | | |
| rust | none: no std JSON (`serde_json` is a crate) | | |

The go cell decodes as the `json` row does, with a `json.Decoder` and `UseNumber`, so numbers stay text as in Mk and `json.Marshal` writes them back verbatim. For a `[]byte` already in memory Go's usual call is `json.Unmarshal`, which has no `UseNumber`: it parses each number to a `float64`, and `Marshal` must then format it. The Decoder costs a `json.Decoder` and a `bytes.Reader` per decode, two decodes per document. Counted, not timed (2026-11-15, `runtime.MemStats` over all 312,017 documents, the full per-document path): the Decoder path makes 102.6 allocations and 4,267 bytes a document, and `Unmarshal` with a `float64` case in `walk` 79.3 allocations and 2,177 bytes, with the same checksum. Which of the two is faster is not measured; the cell keeps the `json` row's form, for parity with it.

Sizes: quick `{data} 1`, publish `{data} 3`. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size json-small` on a quiet machine sets it. Overflow class `wraps`.
