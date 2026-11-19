# strops-find (idiomatic, substring search)

**Task.** Find every non-overlapping occurrence of 8 fixed needles in each line of a 50 MB text with each language's standard substring search. This row prices `str.Find`. `strops-find <file> <rounds>`; the file is `wordfreq`'s text (`tools/bench gen` kind `words`, seed 3: lowercase ASCII words, one space or newline after each), read once:

```
needles = "the", "ing", "qzx", "e a", "tion", "s an d", "abcdefgh", "xylophone quartz"
total = 0
for r in 0 .. rounds-1:
    for line in lines of text:                       (split at '\n'; an empty line finds nothing)
        for nd in needles:
            p = 0
            while q = std find of nd in line, from byte p:
                total = total + q + 1                (q is the offset in the line; wrapping)
                p = q + len(nd)                      (non-overlapping)
```

Every library returns the first match at or after `p`, so all agree on every `q`. The needles run from 3 to 16 bytes, two of them across a word boundary. The text is random letters, so matches are rare: over the generated file, `grep -oF <needle> wordfreq-50mb.txt | wc -l` gives `the` 480, `ing` 1939, `qzx` 1409, `e a` 8931, `tion` 0, `s an d` 1, `abcdefgh` 0 and `xylophone quartz` 0, which is 12,760 matches against 4.44 million find calls a round (8 needles over 555,270 lines), and `grep -cF` with the 8 needles gives 12,484 lines that hold any match (2026-11-15, the file's SHA-256 256d5f7f...5ef44). So the row is a short-haystack row that mostly prices a library's scan of a line that holds no match; the match path is a small share of the calls. The file has 555,270 lines of 89 bytes on average (counted by `awk '{n++; s+=length($0)} END{print n, s/n}'` over the generated file, 2026-11-15), so the per-call cost of each search is in the time.

**Cells.**

| Column | Search | Lines |
|---|---|---|
| mk | `str.Find` on `line[p..]` | `str.Lines` |
| c | `strstr` on NUL-terminated lines (labelled: each `\n` of a copy of the text is overwritten with NUL once, before the rounds) | the NULs |
| rust, rust-ovf | `str::find(&str)` on `&line[p..]` (Two-Way) | `str::lines` |
| zig-fast, zig-safe | `std.mem.findPos` (`indexOfPos`) | `std.mem.splitScalar(u8, text, '\n')` |
| go | `strings.Index` on `line[p:]` | `strings.SplitSeq(text, "\n")` |

Sizes: the 50 MB file; quick 1 round, publish 10 rounds. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size strops-find` on a quiet machine sets it. Overflow class `wraps` (the checksum).
