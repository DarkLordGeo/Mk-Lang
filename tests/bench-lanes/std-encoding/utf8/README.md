# encoding-utf8 (idiomatic table)

Task: `encoding-utf8 <n> <rounds>`. The line `Hello, wörld! Привет мир. 水は冷たい 😀\n` (ASCII, Latin-1, Cyrillic, CJK and one astral scalar, 57 bytes) is repeated `n` times. Then, for `p = 996, 996 + 997, ...` below the length, the first byte at or after `p` that is below 0x80 is replaced by 0xFF. Per round `r`: decode the bytes to valid UTF-8, each 0xFF becoming U+FFFD, and add to a wrapping `u64` sum the output's length and its byte at `(r * 7919) % length`. The program prints the sum.

Checksums: quick (`100 100`) `585974`, publish (`100000 200`) `1142315565`.

Columns: Mk `encoding.Utf8.Decode`; Rust `String::from_utf8_lossy`; Zig `std.unicode.fmtUtf8`; Go `strings.ToValidUTF8`. C has no lossy UTF-8 decoder in its standard library.

Allocation: every column allocates its output afresh each round and frees it (Mk a new `Arena` per round, Zig an allocation per round, Rust's owned `String`, Go's new slice), so no column writes into warm pages the others do not have.
