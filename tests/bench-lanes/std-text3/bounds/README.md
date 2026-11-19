# text-bounds (idiomatic table, Mk only)

Task: walk `text.WordBounds(s)` or `text.SentenceBounds(s)` to the end over a generated text. `text-bounds <mode> <mib> <reps> <seed>`. Each pass adds the number of boundaries and the sum of the boundaries' byte offsets (wrapping); the program prints the sum over `reps` passes.

| mode | iterator | text |
|---|---|---|
| `words`, `sentences` | `WordBounds`, `SentenceBounds` | `../../std-text2/utf16/README.md`'s mixed text: splitmix64 from `seed`, at least `mib * 1048576` bytes, 5 of 16 scalars outside ASCII (Latin-1, Cyrillic, CJK, emoji, combining marks). It has no sentence terminator, so `sentences` is one sentence a pass: the scan's cost alone |
| `prosewords`, `prosesentences` | `WordBounds`, `SentenceBounds` | ASCII prose, at least `mib * 1048576` bytes: words of 1 to 10 lower-case letters (the first of a sentence capitalised), a number `d.dd` one word in 16, words joined by `" "` or `", "`, sentences ended by `". "`, `"? "` or `"! "` |
| `closes` | `SentenceBounds` | `"a."`, then `n` closing brackets, a space, `n` digits and `"x"`, `n = mib * 1048576 / 2`: one sentence, decided by SB8's look-ahead to the `x` |

No standard library among C, Rust, Zig and Go has UAX 29 word or sentence segmentation (`bench.json`'s skip names each); ICU, the `unicode-segmentation` crate and `rivo/uniseg` are not std and do not stand in. So each variant is an Mk throughput row with no ratio. `std/std-textwords_bench_test.go` (`TestSTDTEXT_BenchChecksums`) rewrites the generators in Go and requires the quick checksums to be the Go models' sums.
