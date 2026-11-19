# strops-replace (idiomatic, replace all occurrences)

**Task.** Replace every occurrence of three patterns in each line of a 50 MB text, one replacement after the other, with each language's standard replace-all. `strops-replace <file> <rounds>`; the file is `wordfreq`'s text (`tools/bench gen` kind `words`, seed 3), read once:

```
total = 0
for r in 0 .. rounds-1:
    h = FNV-1a 64 offset basis; n = 0
    for line in lines of text:                 (split at '\n'; an empty line changes nothing)
        a = replace all "e" by "EEE" in line       (non-overlapping, left to right)
        b = replace all "s " by "S" in a
        c = replace all " a" by "_A" in b
        for byte in c: h = (h xor byte) * FNV prime        (wrapping)
        n = n + len(c)
    total = total + h + n
```

Every library replaces non-overlapping matches from the left, so all write the same bytes. One replacement lengthens the line, one shortens it and one keeps its length; one pattern is a single byte and two are two bytes, each a letter and a space.

**Match density.** The text is random lowercase letters in words, so whole English words almost never occur in it; the patterns are a letter and two letter-and-space pairs, so that about 45% of calls replace something, where the words first chosen (`the`, `ing`, `e a`) matched on about 2% of lines. Counted over the generated file (2026-11-15, the file's SHA-256 256d5f7f...5ef44; `grep -oF <p> | wc -l` for matches and `grep -cF <p>` for lines, the third over the second stage's output, made with `sed 's/e/EEE/g; s/s /S/g'`):

| Stage | Matches | Lines with a match, of 555,270 |
|---|---|---|
| `e` | 1,667,919 | 427,193 |
| `s ` | 230,445 | 162,950 |
| ` a` | 220,982 | 157,845 |

So about 55% of the 1.67 million calls a round find nothing (128,077 + 392,320 + 397,425 calls), and the libraries differ there:
- Go's `strings.ReplaceAll` counts the matches first and returns its input, without allocating, when there are none (Go 1.27.3 src/strings/strings.go, `Replace`: "avoid allocation");
- Rust's `str::replace` builds a new `String` on every call (Rust 1.99.1's library/alloc/src/str.rs, `replace`: `String::with_capacity` of the input's length for stages 1 and 3 and of 0 for stage 2, whose pattern is longer than its replacement, then `push_str` of every piece and of the tail, so a non-empty line allocates even with no match);
- Zig's `std.mem.replaceOwned` allocates `replacementSize` bytes on every call (lib/std/mem.zig, `replaceOwned`).

Each is its own std's replace, so the row measures it as it is; the no-match path is part of the price.

**Cells.**

| Column | Replace | Lines |
|---|---|---|
| mk | `str.Replace(&*s, old, new)` (a new copy in a scratch arena `s`, reset after each line) | `str.Lines` |
| rust, rust-ovf | `str::replace` (a new `String`) | `str::lines` |
| zig-fast, zig-safe | `std.mem.replaceOwned(u8, gpa, ...)` over `std.heap.smp_allocator`, freed after the line | `std.mem.splitScalar` |
| go | `strings.ReplaceAll` | `strings.SplitSeq` |
| c | none: no std replace | |

The Mk cell calls std's literal replace, `str.Replace`, which copies the text into its arena even when nothing matches, as Rust's does. The copies of one line go into one scratch arena, made before the rounds and reset after the line, as the twins free theirs after use. At the quick size it prints the twins' checksum (2026-11-16, `tools/bench verify strops-replace`; untimed).

Before std had a replace, the cell was pending (README "No stand-ins"): a replace written from `str.Find` and `Text.Push` would have been a hand-written stand-in, and `std.regex`'s `Regex.Replace`, which gives the same checksum, runs a regex engine where every twin calls a literal replace.

Sizes: the 50 MB file; quick 1 round, publish 5 rounds. The publish size is not yet checked against the 1 s floor: `tools/bench verify -publish-size strops-replace` on a quiet machine sets it. Overflow class `wraps`.
