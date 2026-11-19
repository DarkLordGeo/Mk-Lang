# strops-split (idiomatic, split, trim and words)

**Task.** Split a 50 MB text into pieces at a separator, trim each piece, and split it into whitespace-separated words, with each language's standard string iterators. This row prices `Split`, `Words` and `Trim`. `strops-split <file> <rounds>`; the file is `wordfreq`'s text (`tools/bench gen` kind `words`, seed 3: lowercase ASCII words, one space or newline after each), read once:

```
total = 0
for r in 0 .. rounds-1:
    h = FNV-1a 64 offset basis
    for piece in split(text, "e"):            (every piece, empty ones included)
        t = trim(piece)                        (strips the spaces and newlines at both ends)
        total = total + 1
        h = (h xor len(t)) * FNV prime         (wrapping)
    total = total + h
    for w in words(text):                      (runs of non-whitespace)
        total = total + len(w) + 1
```

Splitting at `"e"` gives 1,667,920 pieces of about 30 bytes (the file's 50,000,001 bytes hold 1,667,919 `e`, counted by `tr -cd e < file | wc -c`, 2026-11-15) that start and end inside a word or at a space, so the trim has work at both ends. Every library keeps empty pieces between two adjacent separators and at the ends, so the piece count agrees. The text holds only ASCII letters, spaces and newlines, so every column's trim strips the same bytes (Zig's trims an ASCII set it is given; the others trim whitespace) and every word iterator splits at the same bytes.

**Cells.**

| Column | Split | Trim | Words |
|---|---|---|---|
| mk | `str.Split("e")` | `str.Trim` | `str.Words` |
| rust, rust-ovf | `str::split('e')` | `str::trim` | `str::split_whitespace` |
| zig-fast, zig-safe | `std.mem.splitScalar(u8, text, 'e')` | `std.mem.trim(u8, s, " \t\r\n")` | `std.mem.tokenizeAny(u8, text, " \t\r\n")` |
| go | `strings.SplitSeq(text, "e")` | `strings.TrimSpace` | `strings.FieldsSeq` |
| c | none: no std split (`strtok` writes its input and is not reentrant) | | |

Sizes: the 50 MB file; quick 1 round, publish 10 rounds. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size strops-split` on a quiet machine sets it. Overflow class `wraps` (the checksum).
