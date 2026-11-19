# textascii (Mk only)

Task: one `std.text` function over mostly-ASCII text. `textascii <mode> <mib> <reps> <seed>`. The text is generated in the program from splitmix64(`seed`), at least `mib * 1048576` bytes. Of 256 draws, 250 are ASCII (lower-case letters, an upper-case one in seven, spaces, digits, punctuation, a newline in 64) and 6 are one non-ASCII scalar each (Latin-1 letters that decompose, Cyrillic, CJK, an emoji, a combining mark), so ASCII runs average about 40 bytes, as in English prose or source code with a few accented names.

Each pass `r` in `0..reps` adds to the sum (wrapping), as `../../std-text2/unicode` does:

| mode | call | adds |
|---|---|---|
| `graphemes` | `text.Graphemes(s)` walked to the end | clusters + bytes of the last cluster |
| `width` | `text.Width(s)` | the width |
| `nfc` | `text.Normalize(&ar, s, Nfc)` | `len(t) + t[r % len(t)]` |
| `fold` | `text.FoldCase(&ar, s)` | `len(t) + t[r % len(t)]` |

This row exists for `tools/bench compare` of std.text's ASCII fast paths (16 bytes a vector in `Width`, `FoldCase` and Normalize's `Span`) against the tree before them. No standard library among C, Rust, Zig and Go has these facilities (`bench.json`'s skip names each), so it is never a cross-language row.
