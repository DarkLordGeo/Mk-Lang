# encoding-base64mime (idiomatic table)

Task: `encoding-base64mime <n> <rounds> <cols>`. `n` bytes of splitmix64 output (seed 1, the bytes of each 64-bit word lowest first, as `encoding-base64`) are encoded once with RFC 4648's standard padded alphabet, and the text is broken into lines of `cols` characters, each ending in CRLF (`cols` 0: one line, no break). Per round `r`: decode the text with the language's standard decoder that skips line breaks into one buffer made before the loop, and add to a wrapping `u64` sum the decoded length and the decoded byte at `(r * 7919) % n`. The program prints the sum.

Variants: `mime76` (76-column CRLF lines, RFC 2045) and `clean` (no break, a data: URL's payload).

Checksums (both variants): quick (`10000 100`) `1013323`, publish (`1000000 2000`) `2000253596`.

Columns: Mk `encoding.Base64DecodeTo` with `Base64Forgiving`; Go `encoding/base64.StdEncoding.Decode` (skips CR and LF); Zig `std.base64.standard.decoderWithIgnore("\r\n")`. C and Rust have no base64 in their standard libraries.
