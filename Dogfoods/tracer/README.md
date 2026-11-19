# tracer

A ray tracer and a function plotter that write PPM images. It shows numeric Mk: a path tracer split into bands rendered by tasks with the same bytes on any number of carriers, a plotter in `f64` or `f32`, and table modes that print `std.math` results with their bits so two builds compare by `diff`.

## Running it

From the program's folder, `table` prints functions of `std.math` over their edge inputs:

```
cd Dogfoods/tracer
mk run -- table sin fma ldexp frexp isnan
```

```text
f64 sin(0) = 0 0000000000000000
f64 sin(-0) = -0 8000000000000000
f64 sin(5e-324) = 5e-324 0000000000000001
f64 sin(2.225073858507201e-308) = 2.225073858507201e-308 000fffffffffffff
f64 sin(2.2250738585072014e-308) = 2.2250738585072014e-308 0010000000000000
f64 sin(1.7976931348623157e+308) = 0.004961954789184062 3f7452fc98b34e97
f64 sin(inf) = nan 7ff8000000000000
f64 sin(-inf) = nan 7ff8000000000000
f64 sin(nan) = nan 7ff8000000000000
f64 sin(0.5) = 0.479425538604203 3fdeaee8744b05f0
...
```

| Command | Does |
|---|---|
| `tracer render SCENE OUT` | Traces a scene file into a PPM image (`--samples`, `--size`, `--band`, `--ascii` for P3, `--16` for 16-bit samples) |
| `tracer plot EXPR OUT` | Draws `y = f(x)` (`--from`, `--to`, `--ymin`, `--ymax`; `--f32` computes entirely in `f32`) |
| `tracer table [NAME...]` | Every plot function, or the ones named, over its edge inputs, with the bits of each result, in `f64` and `f32` (`--f64` or `--f32` for one) |
| `tracer bits` | The same for the integer bit operations |
| `tracer --help` | The commands and options |

`--max-time MS` stops a render, `--mem-limit BYTES` caps the memory for the scene and its grid.

Each image command prints its size, aspect ratio, byte count and the 64-bit FNV-1a checksum of the file on standard error. `OUT` may be `-` for standard output.

Exit status: 0 done, 1 bad input or a failed write, 2 a bad command line, 3 out of memory (the scene's memory is capped by `--mem-limit`), 4 stopped by `--max-time`.

`--seed N` writes the image to a simulated disk (`std.sim`) instead: the disk crashes after a seeded number of operations and restarts, and the program checks that the output is either the whole image or absent. With `--fault nospace` or `--fault denied` the first write fails instead, and the error is reported.

### The scene file

One directive a line, fields separated by spaces, `#` to the end of a line a comment. Later lines override earlier ones; materials are defined before they are used. Paths are relative to the scene file.

| Directive | Fields |
|---|---|
| `size W H` | Image size, 1 to 4096 each (default 64 48) |
| `samples N` | Samples per pixel, 1 to 4096 (default 4) |
| `depth N` | Bounces, 1 to 64 (default 4) |
| `seed N` | The sampler's seed (default 1) |
| `grid N` | Grid cells per axis for the meshes, 0 for automatic; at most 2^24 cells in all |
| `background R G B` | The colour of rays that hit nothing |
| `camera EX EY EZ TX TY TZ FOV [APERTURE FOCUS]` | Eye, target, vertical field of view in degrees, and for depth of field the lens diameter and the focus distance |
| `light X Y Z RADIUS R G B` | A spherical light; a larger radius gives softer shadows |
| `material NAME diffuse R G B` | Lambertian colour |
| `material NAME checker R G B R G B SCALE` | Two colours in squares of `1/SCALE` in texture space |
| `material NAME mirror R G B FUZZ` | Tinted mirror, blurred by `FUZZ` from 0 to 1 |
| `material NAME texture FILE.ppm` | A P3 or P6 image, 8 or 16 bits |
| `sphere X Y Z RADIUS MATERIAL` | |
| `plane PX PY PZ NX NY NZ MATERIAL` | Through a point, with a normal |
| `mesh FILE.obj MATERIAL SCALE TURN TX TY TZ` | An OBJ mesh scaled, turned `TURN` degrees about +y, then moved |

OBJ files: `v`, `vt` and `f` statements, faces of three or more corners (split into a fan), corners written `v`, `v/t`, `v//n` or `v/t/n`, negative indices counting back from the last vertex; other statements are skipped.

### Plot expressions

Numbers, `x`, the constants `pi e ln2 sqrt2 inf nan`, `+ - * /`, `^` (power, right-associative, binding tighter than unary minus), parentheses, and the functions `sqrt floor ceil round roundeven trunc fract exp exp2 expm1 log log2 log10 log1p sin cos tan asin acos atan sinh cosh tanh asinh acosh atanh cbrt` of one argument, `mod pow atan2 hypot copysign nextafter ldexp` of two, `fma` of three, `isnan isinf isfinite` (1 or 0), and `frexp` and `frexpexp` (the fraction and the exponent). With `--f32` every literal, operation and function is `f32`.

## Design

- Every random number is a hash of the scene seed, the pixel and the sample index (SplitMix64's finalizer), so no state passes between pixels and an image has the same bytes on any number of carriers, on every target and in every profile.
- The image is split into bands rendered by tasks; `--max-time` cancels the remaining bands through a scope deadline.
- Mesh triangles sit in a hashed uniform grid walked with the Amanatides and Woo traversal, so a fine grid over a sparse mesh costs memory in proportion to the triangles.
- A plot expression compiles to nodes in post-order and evaluates in one pass with no recursion, in `f64` or `f32`.
- With `--mem-limit` the scene and its grid live in a `mem.Fixed` buffer, so an oversized scene fails with exit 3 instead of taking the machine's memory.
- Images are written beside the target, synced, then renamed over it, so a reader never sees half an image; under `--seed` the same write runs on a `std.sim` disk with crashes and faults.

## Limits

- One bounce of diffuse light per level and no refraction; a render is noisy at low sample counts.
- Textures are sampled at the nearest texel.
- The grid hashes cells into a table of at most 2^22 slots.
- `--max-time` takes milliseconds.

## Tests

The cases in `cases/` check images by checksum (spheres, a mesh, 8- and 16-bit and ASCII output, 1 and 16 carriers), plots, the tables by bits, bad scenes, meshes, textures and options, a memory limit, a deadline, and the atomic write on a simulated disk over many seeds. Run them from the repository root with `go run ./tools/dogfood run -only tracer`.

## Section 18 lines
The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
f64.Bits(self): u64
f64.FromBits(b: u64): f64
f32.FromBits(b: u32): f32
// std.math
math.Floor(x: f64): f64
math.Ceil(x: f64): f64
math.Round(x: f64): f64
math.RoundEven(x: f64): f64
math.RoundToZero(x: f64): f64
math.Fract(x: f64): f64
math.Mod(x: f64, y: f64): f64
math.Pow(x: f64, y: f64): f64
math.Exp(x: f64): f64
math.Exp2(x: f64): f64
math.Expm1(x: f64): f64
math.Log(x: f64): f64
math.Log2(x: f64): f64
math.Log10(x: f64): f64
math.Log1p(x: f64): f64
math.Sin(x: f64): f64
math.Cos(x: f64): f64
math.Tan(x: f64): f64
math.Asin(x: f64): f64
math.Acos(x: f64): f64
math.Atan(x: f64): f64
math.Atan2(y: f64, x: f64): f64
math.Sinh(x: f64): f64
math.Cosh(x: f64): f64
math.Tanh(x: f64): f64
math.Asinh(x: f64): f64
math.Acosh(x: f64): f64
math.Atanh(x: f64): f64
math.Hypot(x: f64, y: f64): f64
math.Cbrt(x: f64): f64
math.Fma(x: f64, y: f64, z: f64): f64
math.CopySign(x: f64, sign: f64): f64
math.FrExp(x: f64): (frac: f64, exp: int)
math.LdExp(frac: f64, exp: int): f64
math.NextAfter(x: f64, toward: f64): f64
math.IsNan(x: f64): bool
math.IsInf(x: f64): bool
math.IsFinite(x: f64): bool
math.SqrtF32(x: f32): f32
math.FloorF32(x: f32): f32
math.CeilF32(x: f32): f32
math.RoundF32(x: f32): f32
math.RoundEvenF32(x: f32): f32
math.RoundToZeroF32(x: f32): f32
math.FractF32(x: f32): f32
math.ModF32(x: f32, y: f32): f32
math.PowF32(x: f32, y: f32): f32
math.ExpF32(x: f32): f32
math.Exp2F32(x: f32): f32
math.Expm1F32(x: f32): f32
math.LogF32(x: f32): f32
math.Log2F32(x: f32): f32
math.Log10F32(x: f32): f32
math.Log1pF32(x: f32): f32
math.SinF32(x: f32): f32
math.CosF32(x: f32): f32
math.TanF32(x: f32): f32
math.AsinF32(x: f32): f32
math.AcosF32(x: f32): f32
math.AtanF32(x: f32): f32
math.Atan2F32(y: f32, x: f32): f32
math.SinhF32(x: f32): f32
math.CoshF32(x: f32): f32
math.TanhF32(x: f32): f32
math.AsinhF32(x: f32): f32
math.AcoshF32(x: f32): f32
math.AtanhF32(x: f32): f32
math.HypotF32(x: f32, y: f32): f32
math.CbrtF32(x: f32): f32
math.FmaF32(x: f32, y: f32, z: f32): f32
math.CopySignF32(x: f32, sign: f32): f32
math.FrExpF32(x: f32): (frac: f32, exp: int)
math.LdExpF32(frac: f32, exp: int): f32
math.NextAfterF32(x: f32, toward: f32): f32
math.IsNanF32(x: f32): bool
math.IsInfF32(x: f32): bool
math.IsFiniteF32(x: f32): bool
math.LeadingZeros64(x: u64): int
math.LeadingZeros32(x: u32): int
math.LeadingZeros16(x: u16): int
math.LeadingZeros8(x: u8): int
math.TrailingZeros64(x: u64): int
math.TrailingZeros32(x: u32): int
math.TrailingZeros16(x: u16): int
math.TrailingZeros8(x: u8): int
math.OnesCount64(x: u64): int
math.OnesCount32(x: u32): int
math.OnesCount16(x: u16): int
math.OnesCount8(x: u8): int
math.RotateLeft64(x: u64, n: int): u64
math.RotateLeft32(x: u32, n: int): u32
math.RotateLeft16(x: u16, n: int): u16
math.RotateLeft8(x: u8, n: int): u8
math.RotateRight64(x: u64, n: int): u64
math.RotateRight32(x: u32, n: int): u32
math.RotateRight16(x: u16, n: int): u16
math.RotateRight8(x: u8, n: int): u8
math.ReverseBits64(x: u64): u64
math.ReverseBits32(x: u32): u32
math.ReverseBits16(x: u16): u16
math.ReverseBits8(x: u8): u8
math.ReverseBytes64(x: u64): u64
math.ReverseBytes32(x: u32): u32
math.ReverseBytes16(x: u16): u16
math.Gcd(a: u64, b: u64): u64
math.CheckedPow(x: i64, n: u32): ?i64
math.CheckedPowU64(x: u64, n: u32): ?u64
```
