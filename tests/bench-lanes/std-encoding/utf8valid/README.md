# encoding-utf8valid (idiomatic table)

Task: `encoding-utf8valid <n> <rounds>`. The line `Hello, wörld! Привет мир. 水は冷たい 😀\n` (ASCII, Latin-1, Cyrillic, CJK and one astral scalar, 57 bytes) is repeated `n` times. Per round `r`: decode the bytes to an owned UTF-8 string (they are valid, so the decoders check and copy them) and add to a wrapping `u64` sum the output's length and its byte at `(r * 7919) % length`. The program prints the sum.

Checksums: quick (`100 100`) `584216`, publish (`100000 200`) `1140028911`.

Columns: Mk `encoding.Utf8.Decode`; Rust `String::from_utf8_lossy(..).into_owned()`; Zig `std.unicode.utf8ValidateSlice` and a copy; Go `bytes.ToValidUTF8`. C has no UTF-8 validator in its standard library. `encoding-utf8` is the same decoders on text holding a lone 0xFF about every 997 bytes.

Allocation: every column allocates its output afresh each round and frees it (Mk a new `Arena` per round, Zig an allocation per round, Rust's owned `String`, Go's new slice), so no column writes into warm pages the others do not have.
