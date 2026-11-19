# casemap (idiomatic, Unicode lower-case mapping)

**Task.** Lower-case every line of a mixed-script text with each language's standard Unicode case mapping. This row prices `str.ToLower`. `casemap <kib> <rounds> <seed>`; the text is generated once, inside the program:

```
alphabet = the code points of these inclusive ranges, in order:
    41-5A 61-7A (ASCII)  C0-D6 D8-DE E0-F6 F8-FE (Latin-1)
    391-3A1 3A4-3A9 3B1-3C1 3C4-3C9 (Greek without the sigmas)  410-44F (Cyrillic)
st = seed; text = ""
while len(text) < kib * 1024 bytes:
    st = st + G; letters = mix(st) mod 12 + 1
    repeat letters times: st = st + G; append alphabet[mix(st) mod len(alphabet)]
    st = st + G; append "\n" if mix(st) mod 12 == 0, else " "
append "\n"
total = 0
for r in 0 .. rounds-1:
    h = FNV-1a 64 offset basis; n = 0
    for line in lines of text:
        lo = std lower-case of line
        for b in bytes of lo: h = (h xor b) * FNV prime      (wrapping)
        n = n + len(lo)
    total = total + h + n
```

`G` and `mix` are splitmix64's (tests/bench/README.md, "Inputs"). About a quarter of the scalars are ASCII, the rest two bytes in UTF-8. The alphabet leaves out every scalar whose lower case differs between Unicode's simple mapping (Mk, Go) and its full, context-sensitive one (Rust): U+0130 and the Greek sigmas (final sigma). So every column writes the same bytes.

**Cells.**

| Column | Mapping | Result |
|---|---|---|
| mk | `str.ToLower(a)` (one scalar to one) | a `str` in one arena, reset each round |
| rust, rust-ovf | `str::to_lowercase` (full mapping) | a `String`, dropped after it is hashed |
| go | `strings.ToLower` (`unicode.ToLower` per rune) | a `string`, garbage collected |
| zig | none: `std.ascii` maps ASCII only, and Zig's std has no Unicode case mapping | |
| c | none: `tolower` maps one byte of the current locale | |

**Case folding is not in this row.** Mk's `text.FoldCase` is full default folding (`"Straße"` to `"strasse"`). Rust's std has no case folding (only `eq_ignore_ascii_case`), Zig's none, and Go has only simple folding as a comparison (`strings.EqualFold`, `unicode.SimpleFold`), a different operation that allocates nothing. No std of another language does what `FoldCase` does, so it would be a Mk-only row; `std-text2/text-unicode`'s `fold` variant already is one.

Sizes: quick `4096 1 1` (4 MiB of text), publish `4096 40 1`. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size casemap` sets it. Overflow class `wraps`.
