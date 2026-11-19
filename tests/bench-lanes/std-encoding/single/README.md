# encoding-single (idiomatic table, Mk only)

Task: `encoding-single <n> <rounds>`. The windows-1252 bytes of the line `Café crème brûlée, naïve “quoted” – € 5, © 2026\n` (48 bytes, 10 of them above 0x7F) are repeated `n` times. Per round `r`: decode the bytes to UTF-8 and add to a wrapping `u64` sum the output's length and its byte at `(r * 7919) % length`. The program prints the sum.

Checksums: quick (`100 100`) `631621`, publish (`100000 200`) `1240022599`.

Columns: Mk `encoding.Windows1252.Decode` only. No standard library of C, Rust, Zig or Go has a single-byte decoder, so each of those cells says so (`skip` in bench.json) and the row has no ratio; it records Mk's throughput. The C, Rust and Go cells hold no hand-written table loops: that would put a library against a loop in the idiomatic table (two tables, never mixed).
