# validutf8 (SIMD)

**Kernel.** Validate UTF-8 with each language's SIMD layer (`ValidUtf8` over 1 GB). `validutf8 <size> <reps> <seed>`:

```
ENC[k], k in 0..15, is 1 + k / 4 bytes:
    61  20  30  7E                                       (ASCII)
    C2 80  DF BF  C3 A9  D0 96                           (U+0080, U+07FF, U+00E9, U+0416)
    E0 A0 80  ED 9F BF  EE 80 80  E4 B8 96               (U+0800, U+D7FF, U+E000, U+4E16)
    F0 90 80 80  F4 8F BF BF  F0 9F 98 80  F3 A0 80 80   (U+10000, U+10FFFF, U+1F600, U+E0000)
P = min(size, 65536); st = seed; p = 0
loop:                                                    (one block of P bytes)
    st = st + G; k = mix(st) & 15; l = 1 + k / 4
    if p + l > P: stop
    blk[p .. p + l] = ENC[k][0 .. l]; p = p + l
blk[p .. P] = 'a'
buf = blk repeated while a whole block fits, then 'a' up to size
total = 0                                                (u64, wrapping)
reps times:
    st = st + G; z = mix(st)
    pos = size - 1 - (z >> 1) mod min(size, 64)
    old = buf[pos]
    if z & 1: buf[pos] = z >> 56                         (a random byte, which may or may not break the text)
    total = total * 31 + (valid_utf8(buf) ? 1 : 0)
    buf[pos] = old
print total
```

`G` is `0x9e3779b97f4a7c15` and `mix` is splitmix64's finaliser, as in every row. The text averages 2.5 bytes a character with no ASCII run longer than chance gives, and its encodings sit on the edges of Unicode's table 3-7 (the smallest and largest two-, three- and four-byte forms, both sides of the surrogate gap), so every validator takes its multibyte path everywhere. The planted byte lands within the last 64 bytes, so each validation reads the whole buffer; whether it breaks the text depends on the byte and where it lands, so the checksum folds every validation's answer, in order, and checks that every column agrees on each of them. Validity is Unicode §3.9 (no overlong forms, no surrogates, nothing above U+10FFFF, no truncated sequence) in every column.

**Fixed targets, no dispatch; sizes**, as in `../indexbyte/README.md`: v2 and v3, with every column built for that one target. L1: 16 KiB, 900,000 validations at v2 and 1,750,000 at v3. L3: 32 MiB, 400 and 1,000. DRAM: 1 GiB, 12 and 23. The counts were set so the fastest cell of each variant ran at least about 1 s in a verify run on a shared machine (2026-11-07). GB/s counts `size * reps` bytes over the median kernel time: the fill (one 64 KiB block generated, then copied) and its page faults are timed by the variant's `baseline` (reps 0) and subtracted. The buffer starts on a 64-byte boundary in the c, rust, zig and go columns, each refusing to run otherwise; the mk column's cells (std's `str.From`, which loads eight bytes at a time below 256 bytes and above that reaches `std.simd.ValidUtf8`'s vector loads, and the width-fixed vector cells) read an arena slice whose alignment Mk code cannot set without `std.raw` (the labelled exception to aligned buffers, the mk row below). Quick: 100,000 bytes, 10 validations.

**std's cells at the L1 v2 size only.** The Mk and Go cells are std's validators as they ship: Go's is scalar, and Mk's `str.From` is scalar below 256 bytes and dispatching above (below). At the other sizes Go's would run 10 to 30 times the fastest cell (each publish run of a cell is timed 39 times), and Mk's picks its vector form at run time, not from the variant's target, so they are in `validutf8-l1v2` only and the other variants name them with that reason (the variant's `skip`). The 16 KiB buffer repeats, and branch predictors partly learn it: in the size check Go's `utf8.Valid` ran at about twice its 32 MiB rate at 16 KiB, so the L1 rate flatters a branchy validator. `std.simd.ValidUtf8` at every size and target is not measured here.

**Cells.**

| Column | Validator |
|---|---|
| mk | `validutf8-l1v2`: std's `str.From` (`mk/`, `std/core/core_utf8.mk`), labelled. Below 256 bytes it is scalar: ASCII eight bytes a load, every other byte through table 3-7's per-lead ranges. std.core's `ValidUtf8` hands 256 bytes or more to `std.simd.ValidUtf8`, which tests `Cpu()` and runs its 16-, 32- or 64-byte form by the running CPU, not the variant's x86-64-v2 target, so the cell is labelled dispatching: the variant's `dispatching` field, printed in its notes, which `TestNoRunTimeChoiceInFixedRows` requires of any fixed-target cell whose release build reads the CPU's features. The other five variants: the width-fixed vector cells, `mkfix/` (v3, `Vec[u8, 32]`) and `mkfixv2/` (`validutf8-l3v2` and `dramv2`, `Vec[u8, 16]`), std's `ValidUtf8N` body (Keiser-Lemire) without its `Cpu()` test, the same text but `const L`; their quick and publish checksums were checked, untimed (2026-11-15). `mksimd/`, which calls `std.simd.ValidUtf8`, picks its form at run time and from 256 bytes on this AVX-512 machine runs its 64-lane avx512bw form, wider than the other v3 cells, so it is no cell of this table. Their buffer is an arena slice at 16 mod 64 (the labelled exception to aligned buffers): at v3 half of the 32-byte loads cross a cache line, at v2 none |
| c | Keiser and Lemire's lookup algorithm ("Validating UTF-8 In Less Than One Instruction Per Byte", 2021; simdjson's and simdutf's validator) in `<immintrin.h>`: three 16-entry nibble lookups by `pshufb`, the third- and fourth-byte check by saturating subtraction, 64-byte chunks with an all-ASCII skip, the tail zero-padded into one chunk. SSE at v2 (SSSE3 `pshufb`, SSE4.1 `ptest`), AVX2 at v3 (`__AVX2__`) |
| rust, rust-ovf | the same algorithm in `std::arch::x86_64` intrinsics, `cfg(target_feature = "avx2")`; loads in `unsafe` after an `assert!` of their bounds. std's `str::from_utf8` is scalar with a word-at-a-time ASCII skip |
| zig-fast, zig-safe | the same algorithm in `@Vector(W, u8)`, W = `std.simd.suggestVectorLength(u8)`. Zig has no run-time byte shuffle (`@shuffle` takes a comptime mask), so each nibble lookup is a chain of compare-and-select over the table's runs of equal entries (5, 7 and 4 selects); the byte shifts are `@shuffle` with comptime masks, saturating subtraction is `-|`. std's `std.unicode.utf8ValidateSlice` is scalar after an ASCII prefix |
| go | scalar, `unicode/utf8.Valid`, labelled: Go has no SIMD layer a program can write vector code in |

**Checksums.** Quick `27320942869538` in every variant. Publish: l1v2 `17847553385475178960`, l3v2 `4126210834813151499`, dramv2 `26228985616620608`, l1v3 `16670461197503712794`, l3v3 `1597775540998367779`, dramv3 `1619291358942268734`. Before the row was written down, the C validator was checked against a scalar table 3-7 validator on 3,000,000 random strings of these encodings with up to three random bytes overwritten (about a third valid), at v2 and at v3, with no disagreement (a scratch harness, not in the tree).
