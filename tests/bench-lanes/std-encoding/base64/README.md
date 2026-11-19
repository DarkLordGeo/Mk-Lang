# encoding-base64 (idiomatic table)

Task: `encoding-base64 <n> <rounds>`. The input is `n` bytes of splitmix64 output: `st = 1`; per 8 bytes `st += 0x9e3779b97f4a7c15`, `z = mix(st)` (`intsum`'s finalizer), bytes `z >> 0, z >> 8, ...` little-endian, the last word cut at `n`. Per round `r` in `0..rounds`: encode the input with the standard padded alphabet (RFC 4648 section 4), decode the text back into a buffer, and add to a wrapping `u64` sum the text's length, the decoded length and the decoded byte at `(r * 7919) % n`. The program prints the sum.

Checksums: quick (`10000 100`) `2346923`, publish (`1000000 2000`) `4666925596`.

Columns: Mk `encoding.Base64Encode` (arena, reset each round) and `encoding.Base64DecodeTo`; Go `encoding/base64.StdEncoding.Encode` and `Decode`; Zig `std.base64.standard.Encoder.encode` and `Decoder.decode`. C and Rust have no base64 in their standard libraries. The best-in-class reference for the algorithm is Muła and Lemire's vector codec (aklomp/base64, simdutf), which Mk's path follows at 16 bytes a step; neither is a standard library, so neither is a column.
