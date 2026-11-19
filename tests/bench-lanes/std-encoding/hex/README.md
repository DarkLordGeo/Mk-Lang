# encoding-hex (idiomatic table)

Task: `encoding-hex <n> <rounds>`, the input of `encoding-base64` (n splitmix64 bytes, seed 1). Per round `r`: encode the input as lower-case hex, decode it back, and add to a wrapping `u64` sum the text's length, the decoded length and the decoded byte at `(r * 7919) % n`. The program prints the sum.

Checksums: quick (`10000 100`) `3013323`, publish (`1000000 1000`) `3000121616`.

Columns: Mk `encoding.HexEncode` and `HexDecode` (arena, reset each round); Go `encoding/hex.Encode` and `Decode`; Zig `std.fmt.bufPrint("{x}")` and `std.fmt.hexToBytes`. C and Rust have no hex codec in their standard libraries.
