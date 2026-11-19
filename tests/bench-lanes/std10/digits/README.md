# digits (idiomatic table)

One row with three variants, under tests/bench/README.md's contract for the idiomatic table: each language gets a float's decimal digits from its standard library. Only `tools/bench` publishes a number from it.

## Task

Usage `digits <variant> <reps> <seed>`. From `seed`, splitmix64 gives one word z per input, 4,096 inputs: the f64 with z's mantissa bits and the biased exponent `723 + (z >> 53) mod 601` (binary exponents -300 to 300), or for `fixed` `1003 + (z >> 53) mod 81` (-20 to 60, below the 1e21 up to which ECMA-262's toFixed writes digits), positive. No floating-point arithmetic builds an input, so every language has the same inputs bit for bit.

Then `reps` passes, pass r starting at input r (index `(i + r) & 4095`), each result folded into a wrapping u64 sum: `len * 1000003 + (exp + 400) * 1009 + s`, where the digits are the significand's with leading and trailing zeros dropped, `len` their count, `exp` the scientific exponent (d.ddd * 10^exp) and `s` the sum of every digit but the last. A value rounded away to nothing folds 400 * 1009. The last digit is left out because of exact ties between two shortest candidates: where two digit strings of the shortest length are equally close to the input (both read back to it), Go and Mk break the tie to even and Rust's `flt2dec` breaks it away from even, as at 1686705793782042.25, which Rust gives as 1.6867057937820423e15 and Go and Mk as ...422e15 (2 of the 4,096 inputs). Every pass adds the same sum, so the publish checksum is the quick checksum times reps / 100.

| Variant | Task |
|---|---|
| `shortest` | the shortest digits that read back to the input |
| `exact17` | 17 significant digits of the exact value, ties to even |
| `fixed` | the exact value to 6 digits after the point, ties to even (the fast path up to 18 digits, the exact Decimal past) |

## Columns

- **Mk**: `math.ShortestDigits(x)`, `math.ExactDigits(x, 17, DigitMode.Significant)` and `math.ExactDigits(x, 6, DigitMode.Fraction)` (std/math/digits.mk over std/core/core_fmtfloat.mk's FloatDigits: Go 1.27's unrounded scaling for the shortest digits and for at most 18 digits, else the exact Decimal).
- **C**: `snprintf("%.16e")` and `"%.6f"` (the UCRT and glibc print the exact value correctly rounded); the text is read back. No shortest form (skip).
- **Rust**: `write!("{:e}")`, `"{:.16e}"` and `"{:.6}"` into a reused `String` (core's flt2dec: Grisu with a Dragon fallback, exact for a precision); the text is read back.
- **Zig**: `std.fmt.bufPrint("{e}")` (std.fmt.float, Ryu), shortest only: its precision forms round the shortest digits, not the exact value (skips, measured: `{e:.16}` gives the shortest row's checksum, and `{d:.6}` prints 1e15 + 0.3 as ...0.200000 where the exact value is ...0.25).
- **Go**: `strconv.AppendFloat(buf, x, 'e', -1, 64)`, `'e', 16` and `'f', 6` into a reused buffer; the text is read back.

**The columns do different work.** The text columns generate the digits, lay them out as text, and the program reads the text back; Mk generates the digits alone, as a `Digits` value, which is the function LuxJS calls (its layouts are its own). A published ratio says so.

## Checksums

Every column was built and run on 2026-11-18 (checksums only, no timing): at `100 1` shortest 6884459467300 (Mk, Go, Rust, Zig), exact17 7082060955100 (Mk, Go, C, Rust) and fixed 4896252904300 (Mk, Go, C, Rust); at `1000 1` fixed 48962529043000 (Mk, Go, C, Rust); at `40000 1` shortest 2753783786920000 and exact17 2832824382040000.
