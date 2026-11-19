# text-utf16 (idiomatic table)

Task: convert UTF-8 text to UTF-16 code units (`encode`), or UTF-16 code units back to UTF-8 (`decode`), with each language's standard library. `text-utf16 <encode|decode> <mib> <reps> <seed>`. tools/bench runs the two as the rows `std-text2/text-utf16-encode` and `std-text2/text-utf16-decode`, each with its own checksum and ratio.

Input, built in each program: `st = seed`; until the text holds at least `mib * 1048576` bytes, `st += 0x9e3779b97f4a7c15` (wrapping), `x = mix(st)` (splitmix64's finalizer, `intsum`'s), `k = x % 16`, `y = x >> 8`, and append one scalar:

| `k` | scalar |
|---|---|
| 0 to 9 | `0x61 + y % 26`, or `0x41 + y % 26` when `x % 7 == 0` |
| 10 | `0x20` |
| 11 | `0xC0 + y % 64` (two bytes) |
| 12 | `0x430 + y % 32` (two bytes) |
| 13 | `0x4E00 + y % 0x5000` (three bytes) |
| 14 | `0x1F600 + y % 80` (four bytes, a surrogate pair) |
| 15 | `0x300 + y % 0x70` (two bytes, combining marks) |

So 6 of 16 scalars (37.5 percent) are outside ASCII.

`encode`: each pass `r` in `0..reps` converts the text to `u`, its UTF-16, and adds `len(u) + u[r % len(u)]` to the sum (wrapping).

`decode`: the program converts the text to `u` once, with its own encoder, before the passes; each pass converts `u` back to `back`, its UTF-8, and adds `len(back) + back[r % len(back)]` (a byte) to the sum.

The program prints the sum.

Columns, encode then decode: Mk `text.ToUtf16` and `text.FromUtf16` into an arena reset each pass; C `<uchar.h>` `mbrtoc16` and `c16rtomb`, one character a call, the standard's only form, into a `malloc`'d buffer of the upper bound (`n` units, `3` bytes a unit), freed each pass; Rust `s.encode_utf16().collect::<Vec<u16>>()` and `String::from_utf16`; Zig `std.unicode.utf8ToUtf16LeAlloc` and `utf16LeToUtf8Alloc`; Go `utf16.Encode([]rune(s))` and `string(utf16.Decode(u))`.

On the benchmark target, `x86_64-pc-windows-msvc` with the UCRT, `mbrtoc16` and `c16rtomb` convert UTF-8 whatever the locale (Microsoft's documentation of both functions); on Linux the C program sets a UTF-8 locale and stops if there is none.

No column is told the text is valid beyond what its types say: Mk and Rust take a `str`, so their encoders skip validation; C's and Zig's encoders validate the UTF-8. The Mk, C, Rust and Zig decoders fail on an unpaired surrogate; Go's `utf16.Decode` never fails, it replaces an unpaired surrogate with U+FFFD.
