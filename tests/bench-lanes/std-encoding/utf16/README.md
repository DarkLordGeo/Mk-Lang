# encoding-utf16 (idiomatic table)

Task: `encoding-utf16 <n> <rounds>`. The line `Hello, wörld! Привет мир. 水は冷たい 😀\n` (ASCII, Latin-1, Cyrillic, CJK and one astral scalar) is repeated `n` times and encoded as UTF-16LE before the loop. Per round `r`: decode the bytes to UTF-8 and add to a wrapping `u64` sum the output's length and its byte at `(r * 7919) % length`. The program prints the sum.

Checksums: quick (`100 100`) `584216`, publish (`100000 200`) `1140028911`.

Columns: Mk `encoding.Utf16Le.Decode`; Rust `char::decode_utf16`; Zig `std.unicode.utf16LeToUtf8`; Go `unicode/utf16.DecodeRune` with `unicode/utf8.AppendRune`. C has no UTF-16 decoder in its standard library.
