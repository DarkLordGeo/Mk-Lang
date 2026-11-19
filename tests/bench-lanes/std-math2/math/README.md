# math (std-math2, idiomatic)

One row with a variant per function, under tests/bench/README.md's contract for the idiomatic table: each language calls its standard library's function. Only `tools/bench` publishes a number from it.

## Task

Usage `math <fn> <reps> <seed>`. From `seed`, splitmix64 (Steele, Lea and Flood 2014) gives three words a, b, c per input, 4,096 inputs. An f64 input is built from its word's bits: mantissa `z & (2^52 - 1)`, exponent `lo + (z >> 53) mod (hi - lo + 1)`, and the sign from bit 52 where the range is signed. An f32 input takes the same word with the f32 field widths. No floating-point arithmetic builds an input, so every language has the same inputs bit for bit.

Then `reps` passes over the inputs, pass r starting at input r (index `(i + r) & 4095`), each result's bits folded into a wrapping u64 sum: `bits >> 32` of an f64 result, `bits >> 16` of an f32 result. A result one ulp away from another library's changes the sum only through a carry into the kept bits (about 2^-32 per differing f64 result, 2^-16 per f32 result), so the libraries agree without being identical. Every pass adds the same per-pass sum, so the publish checksum is the quick checksum times reps / 100.

| Variant | Call | x exponents | Other inputs |
|---|---|---|---|
| `exp`, `expf` | e^x | [-10, 5], signed | |
| `log`, `logf` | ln x | [-100, 100] | |
| `sin`, `sinf` | sin x | [-10, 2], signed | |
| `sinlarge` | sin x | [20, 60], signed (Payne-Hanek reduction in Mk) | |
| `pow` | x^y | [-4, 3] | y: [-4, 4], signed |
| `atan2` | atan2(y, x) | [-10, 10], signed | y: same range |
| `sinh` | sinh x | [-10, 4], signed | |
| `cbrt` | cube root | [-200, 200], signed | |
| `fma` | x y + z rounded once | [-5, 5], signed | y, z: same range |

## Columns

- **Mk**: `std.math` (`Exp`, `Log`, `Sin`, `Pow`, `Atan2`, `Sinh`, `Cbrt`, `Fma`, `ExpF32`, `LogF32`, `SinF32`): pure Mk, no cpu dispatch. `Exp`, `Log` and `Pow` are table-driven, after ARM's optimized-routines without a fused multiply-add (std/math/exptab.mk, logtab.mk, pow.mk); `Fma` is software on every target; `Sinh` and `Atan2` use double-double kernels (std/math/dd.mk); the F32 forms evaluate the f64 function (`ExpF32` its kernel directly) and round once, `LogF32` and `SinF32` after a short f64 kernel with a rounding test that answers almost every input (std/math/fast32.mk).
- **C**: `<math.h>` of the platform C library (UCRT on Windows, glibc on Linux), `-ffp-contract=off`. The static UCRT picks an FMA3 and AVX2 body of `exp`, `log`, `pow`, `sin`, `sinh`, `atan2`, `expf`, `logf` and `sinf` (and for `fma` the `vfmadd` instruction) at run time on a CPU that has them (`cbrt` has one body); the table's one CPU target is x86-64-v2, so `main` first turns that choice off with the UCRT's own switch, `_set_FMA3_enable(0)`, and exits with status 3 if `_get_FMA3_enable()` still reports it on. glibc chooses its FMA bodies the same way (ifunc) and has no switch a program can call: on Linux the C cells are FMA3 code until tools/bench sets `GLIBC_TUNABLES` for them.
- **Rust**: `f64`/`f32` methods (`exp`, `ln`, `sin`, `powf`, `atan2`, `sinh`, `cbrt`, `mul_add`); on Windows they reach the same static UCRT as the C column, whose FMA3 paths `main` turns off the same way. That takes the row's one `unsafe extern` block (two UCRT functions declared `safe`), which the idiomatic table's ban on `unsafe` needs an exemption for.
- **Zig**: `@exp`, `@log`, `@sin`, `std.math.pow`, `atan2`, `sinh`, `cbrt`, `@mulAdd`: Zig's own code, no C library.
- **Go**: `math.Exp`, `Log`, `Sin`, `Pow`, `Atan2`, `Sinh`, `Cbrt`, `FMA`; Go has no f32 functions, so the f32 variants convert through f64. `math.Exp` (GOROOT/src/math/exp_amd64.go: `useFMA`) and `math.FMA` (an intrinsic behind `x86HasFMA`) use the FMA instruction chosen at run time, and `Pow`, `Sinh` and the f32 `expf` call `Exp`, so the Go cells of exp, pow, sinh, expf and fma are FMA3 code. `GOAMD64=v2` does not change that. `GODEBUG=cpu.fma=off` does on Linux, but not on Windows: the runtime reads `GODEBUG` before choosing CPU features only on Unix-like systems (runtime/proc.go `getGodebugEarly`), and a test of 200 million `math.FMA` calls took 74 ms without it and 70 ms with it (the hardware path both times). tools/bench also clears `GODEBUG` for every program. So on Windows those five Go cells are FMA3 code with no switch, and they are left out of the row's Mk / best until tools/bench labels them.

Accuracy is not part of this row: each Mk function's error is measured by std's Go tests.
