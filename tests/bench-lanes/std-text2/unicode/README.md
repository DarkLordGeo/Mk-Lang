# text-unicode (idiomatic table, Mk only)

Task: one `std.text` function over generated text. `text-unicode <mode> <mib> <reps> <seed>`. The text is `../utf16/README.md`'s (splitmix64 from `seed`, at least `mib * 1048576` bytes, 6 of 16 scalars outside ASCII: Latin-1 letters that decompose, Cyrillic, CJK, emoji, and combining marks that compose with some of the letters they follow).

Each pass `r` in `0..reps` adds to the sum (wrapping):

| mode | call | adds |
|---|---|---|
| `graphemes` | `text.Graphemes(s)` walked to the end | clusters + bytes of the last cluster |
| `width` | `text.Width(s)` | the width |
| `nfc`, `nfd`, `nfkc`, `nfkd` | `text.Normalize(&ar, s, form)` | `len(t) + t[r % len(t)]` |
| `fold` | `text.FoldCase(&ar, s)` | `len(t) + t[r % len(t)]` |

The arena `ar` is reset after each pass. The program prints the sum.

No standard library among C, Rust, Zig and Go has extended grapheme clusters, normalization, full case folding or terminal width (`bench.json`'s skip names each); ICU, the `unicode-*` crates, `golang.org/x/text` and `rivo/uniseg` are not std and do not stand in. So each variant is an Mk throughput row with no ratio.

Mode `marks` (variants `marks1` and `marks4`) is the adversarial input of canonical ordering: the text is `"a"` then `mib * 1048576` combining marks whose classes cycle 240, 230, 220, 202 (U+0345, U+0301, U+0323, U+0327), one run of non-starters, and each pass adds `len(t) + t[r % len(t)]` of `text.Normalize(&ar, s, Nfd)`. `marks4` has four times the marks of `marks1`, so a linear reorder takes about four times as long; an O(n log n) or O(n log^2 n) sort takes more.
