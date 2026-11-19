# keywords (same algorithm, a perf-codegen row)

**Task.** Classify words by comparing each with 16 keyword literals, the keyword match of a lexer. It measures `str ==` against a literal. `keywords <n> <rounds> <seed>`:

```
K = if else for while return break continue fn let var const match struct enum import export
st = seed
for i in 0 .. n-1:                      the words, one buffer
    st = st + G; z = mix(st)
    kind = z mod 4
    kind 0: append K[(z >> 8) mod 16]   a byte loop over the keyword's bytes
    kind 1: append K[(z >> 8) mod 16] with its last byte replaced by 'a' + (z >> 16) mod 26
    else:   len = 1 + (z >> 8) mod 8; len times: st = st + G; append 'a' + mix(st) mod 26
    word[i] = a view of those bytes (no copy)
total = 0
rounds times: for i in 0 .. n-1: total = total + classify(word[i]) * (i mod 1000 + 1)     (wrapping)
classify(w) = 1 + the first k with w == K[k], tested in K's order, else 0
```

`mix` is splitmix64's finalizer and G is 0x9e3779b97f4a7c15, as in the other rows. Arithmetic types, in every column: n, rounds, the offsets, the lengths and the loop indices are 64-bit signed (Mk's `int`; `i mod 1000` is a signed remainder), the generator state, the classes and the checksum unsigned 64-bit and wrapping.

**Cells.** What each word of the timed loop costs besides the compares:

| Column | Words | Compare | Checks per word |
|---|---|---|---|
| mk | `str` views of one `str` (`str.From` checks the UTF-8 once, before the loop) | `w == "if"`, ... (str's `==`) | `offs[i]` and `offs[i + 1]` bounds, the overflow of `i + 1` and of `i % 1000 + 1`, the `Cast[u64]` range check, the range `a <= b <= len`, and the two UTF-8 boundary bytes at `a` and `b` (a load and a trap each) |
| mk-wrap | as mk (`mkwrap/`) | as mk | as mk without the three arithmetic checks: `i +% 1`, `i % 1000 +% 1` and `Trunc[u64]` wrap; both bounds, the range and both boundary bytes stay |
| rust | `&str` views of one `&str` (`std::str::from_utf8` once) | `w == "if"` | as mk-wrap: both bounds, the range, both char boundaries (`as` never checks); the overflow of `i + 1` and of `i % 1000 + 1` only in `rust-ovf` |
| zig-safe | `[]const u8` views of one slice | `std.mem.eql(u8, w, "if")` | both bounds and the range, the overflow of `i + 1` and of `@rem(i, 1000) + 1`, and the range of the five `@intCast` (`i`, `i + 1`, the two loaded offsets, the weight); no boundary check |
| zig-fast | as zig-safe | as zig-safe | none |
| go | `string` views of one string (one conversion copy) | `w == "if"` | both bounds and the range; no boundary check |
| c | `{offset, length}` into one buffer | `n == 2 && memcmp(p, "if", 2) == 0`, ... | none |

Sizes: quick `100000 3 1`, publish `2000000 50 1` (C about 1 s, AMD Ryzen 7 9800X3D, Windows 11, 2026-11-07, shared machine). Overflow class `wraps` (the checksum).

The Checks column is the checks the source asks for, per column; what each optimizer proves away (for example zig-safe's `@intCast(i)`, nonnegative by the loop) was not read from the disassembly.
