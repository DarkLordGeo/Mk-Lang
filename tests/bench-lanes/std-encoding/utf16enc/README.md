# encoding-utf16enc (idiomatic table)

Task: `encoding-utf16enc <n> <rounds>`. The line `Hello, wörld! Привет мир. 水は冷たい 😀\n` (ASCII, Latin-1, Cyrillic, CJK and one astral scalar) is repeated `n` times. Per round `r`: encode the text as UTF-16LE and add to a wrapping `u64` sum the encoding's length in bytes and its byte at `(r * 7919) % length`. The program prints the sum.

Checksums: quick (`100 100`) `705159`, publish (`100000 200`) `1400009581`.

Columns: Mk `encoding.Utf16Le.Encode`; Rust `str::encode_utf16`; Zig `std.unicode.utf8ToUtf16Le`; Go `unicode/utf16.AppendRune`. C has no UTF-16 encoder in its standard library.
