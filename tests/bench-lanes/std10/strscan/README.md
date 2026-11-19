# strscan (idiomatic table)

One row with four variants under tests/bench/README.md's contract for the idiomatic table: the string kernels LuxJS needs at string creation and in lastIndexOf, each language through its standard library. Only `tools/bench` publishes a number from it.

## Task

Usage `strscan <variant> <size> <reps> <seed>`. The text has `size` bytes: 128 ASCII letters (`'a' + i mod 26`), then pieces chosen by splitmix64 words z from `seed`: for `z mod 8` below 5 the letter `'a' + (z >> 8) mod 26`, 5 `é` (C3 A9), 6 `€` (E2 82 AC), 7 `😀` (F0 9F 98 80), and for validwtf8 a lone surrogate ED A0 80 instead of one `😀` in four (`(z >> 16) mod 4 == 0`); a piece that would not fit is a letter. isascii and lastindexbyte take letters only, and lastindexbyte puts its one 0xFF at byte 100, so the scan from the end crosses 65,436 bytes. Then `reps` calls, call r on the text from offset `r mod 64` (the ASCII prefix makes every offset a scalar boundary), each answer (a bool as 0 or 1, an offset, a count) and the offset added to the checksum.

| Variant | Kernel |
|---|---|
| `isascii` | every byte below 0x80 |
| `lastindexbyte` | the offset of the last 0xFF |
| `utf16len` | the UTF-16 units of the UTF-8 text |
| `validwtf8` | well-formed WTF-8 (lone surrogates allowed) |

## Columns

- **Mk**: `simd.IsAscii`, `LastIndexByte`, `Utf16Len` and `ValidWtf8` (std/simd/scan.mk), each dispatched at run time to its avx512bw (from 256 bytes), avx2 (from 64) or 16-lane form, as std.simd's byte routines are.
- **Rust**: `<[u8]>::is_ascii`, `iter().rposition`, `str::encode_utf16().count()`; no public WTF-8 validator.
- **Zig**: `std.mem.findScalarLast`, `std.unicode.calcUtf16LeLen`, `std.unicode.wtf8ValidateSlice`; no all-ASCII test of a slice.
- **Go**: `bytes.LastIndexByte`, and a range over the string adding `utf16.RuneLen` (no counter in std); no all-ASCII test and no WTF-8 validator.
- **C**: none of the four in std (the UCRT has no memrchr).

**The columns do different work on utf16len.** Zig, Rust and Go decode every scalar; Mk counts bytes and never decodes. The answer is the same on valid UTF-8; a published ratio says so.

## Checksums

Built and run on 2026-11-18 (checksums only, no timing), `65536 1000 1` and `65536 10000 1`: isascii 32020 and 324616 (Mk, Rust), lastindexbyte 100000 and 1000000 (Mk, Go, Rust, Zig), utf16len 42276000 and 422760000 (Mk, Go, Rust, Zig), validwtf8 32020 and 324616 (Mk, Zig). The publish checksums follow by arithmetic: 1,000,000 calls add 1,000,000 answers of 1 (or 100, or 42,276 units) and 31,500,000 of offsets where the answer does not absorb them.
