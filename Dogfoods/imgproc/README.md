# imgproc

An image pipeline over PGM and PPM files: decode, convolve, resize, equalise, threshold and adjust, then encode. Every kernel is written once over `std.simd` vectors and runs at the vector widths and CPU features the machine has, and every path gives the same bytes as the scalar one. It shows `std.simd`, run-time CPU dispatch, parallel tiles under a task scope, and fixed memory budgets.

## Running it

From the program's folder:

```
cd Dogfoods/imgproc
mk run -- info cases/data/photo.ppm gray resize=10x0
```

```text
cases/data/photo.ppm: P5 10x5, 1 channel(s), maxval 255
samples: min 18 max 202 mean 109.920 deviation 47.751
brightest: 1 samples, the first at x 9 y 3
bits used 11111111, always set 00000000; xor 16, sum32 00001578
```

```
imgproc [options] run IN OUT [OP...]     decode IN, apply each OP in order, encode OUT
imgproc [options] info IN [OP...]        the shape and a summary of the samples, after the operations
imgproc [options] compare A B            exit 0 when A and B are the same image, else 1
```

`IN` and `OUT` are PGM (P2, P5) or PPM (P3, P6) files with a maxval up to 255; `OUT` may be `-` for standard output. `IN` may also be `synth:WxH`, a colour test pattern made in memory, for timing a pipeline. `--ascii` writes P2 or P3 instead of P5 or P6, and `--pbm` a P4 bitmap of a gray result.

| Operation | Effect |
|---|---|
| `invert` | `maxval - v` |
| `brighten=D` | `v + D` clamped to 0..maxval, D from -255 to 255 |
| `threshold[=T]` | maxval where `v >= T`, else 0; without T, Otsu's threshold |
| `equalize` | histogram equalisation over all channels together |
| `levels` | the darkest sample to 0, the brightest to maxval |
| `blur`, `sharpen`, `edge`, `emboss`, `box=N` | named 3x3 kernels, and an N by N box |
| `conv=W:W:...[/D][+B]` | a square kernel of integer weights, divisor D (default their sum), bias B |
| `mirror`, `flip` | reverse the columns, or the rows |
| `gray` | a colour image's luma, (77 R + 150 G + 29 B + 128) / 256 |
| `half`, `double` | the 2x2 average rounded half up, or each pixel four times |
| `gain=F` | `v * F` clamped, F from 1/256 to below 256, in steps of 1/256 |
| `sobel[=l1|max]` | the Sobel gradient magnitude: Euclidean (default), the sum or the larger of the two derivatives |
| `resize=WxH` | bilinear resize with pixel centres aligned; a side of 0 keeps the aspect ratio |
| `dilate`, `erode` | the largest or smallest sample of each 3x3 neighbourhood; gray images |
| `swap=bgr|gbr|brg` | reorder a colour image's channels |
| `posterize=B` or `=R:G:B` | keep the top B bits of every channel, or of each, 1 to 8 |
| `gamma=G` | `maxval * (v / maxval)^(1 / G)`, G from 0.1 to 10 in steps of 0.001 |

Options: `--path` picks the kernels (`scalar`, `v128`, `w256`, `w512`, `v256`, `v512`, or `auto`, the default, the widest the CPU runs); `--tasks` caps the tile tasks alive at once and `--tile-rows` sets the rows per tile; `--mem-limit` caps the bytes the images may take; `--timeout-ms` gives up after that long; `--sim-fault none|nospace|denied` writes OUT to a simulated disk whose next write fails as named. `imgproc --help` lists them.

Exit codes: 0 done, 1 the images differ (`compare`), 2 a usage error, 3 bad image data, 4 a file error, 5 out of memory, 6 timed out or cancelled, 7 the path needs a CPU feature this machine lacks.

## Design

- Each operation has one generic body over a `const N` lane count and a scalar version that defines the result. A `Kernels` table of function values holds one path's bodies; the v256 and v512 entry points carry `cpu` clauses.
- `src/dispatch` is the one `unsafe` module: it names the `cpu` functions as values only after `simd.Cpu()` reports their features, or through `simd.Dispatch`.
- Byte identity holds because every per-lane computation is exact: point operations are integer, and a convolution adds products of integers below 2^24, which f32 holds exactly, so no lane order or `Fma` can change a sum. Resize and gamma run the same f32 operations in the same order on every path.
- `std.math` has no power, logarithm or exponential, so gamma builds its table from the f32 representation (`Reinterpret` in lanes, `f32.Bits` and `f32.FromBits` in the scalar body).
- Each operation runs in tiles of rows: `ChunksMut` cuts the output, one task per tile under `io.ScopeWith(Limits(...))`, scratch from a `mem.Fixed` over a buffer on the task's stack, and a deadline or a failed tile cancels the scope.
- The options come from `gen cli.Args`; every failure becomes a message and an exit code.

## Limits

- Samples are 8-bit: a maxval above 255 is refused.
- Equalisation uses one histogram for all channels of a colour image.
- Resize is bilinear: shrinking by more than half skips source pixels (use `half` first).
- Gamma's table is computed by series accurate to about 1e-5, not by a correctly rounded power, so an entry whose exact value lies that close to a half step can round the other way.
- The v256 and v512 paths need x86-64-v3 and x86-64-v4; on another CPU `--path v256` exits 7, and `auto` falls back.

## Tests

The cases in `cases/` run each operation chain on every path with one shared expected output, hand-computed results on tiny images, bad headers and samples, memory limits, a zero deadline, and a full or read-only simulated disk. Run them from the repository root with `go run ./tools/dogfood run -only imgproc`.

## Section 18 lines

The standard-library lines this program uses, as `doc/reference.md` lists them; `go run ./tools/dogfood cover` reads this block.

```
f32.Bits(self): u32
f32.FromBits(b: u32): f32
Slice[T].SplitAt(mut self, i: int): (left: mut []T, right: mut []T) from self
Slice[T].ChunksMut(mut self, n: int): ChunksMut[T] from self
ChunksMut[T].Next(mut self): ?mut []T from self.ptr
Arena.Slice[T](&self, n: int): OutOfMemory!mut []T
struct Limits {
enum StackSize {
enum Arch {

// std.math
math.Min[T: Ord + Plain](a: T, b: T): T
math.Max[T: Ord + Plain](a: T, b: T): T
math.Abs(x: f64): f64
math.Sqrt(x: f64): f64

// std.mem
Fixed.New(buf: mut []u8): Fixed
Fixed.Alloc(&self): &Alloc from self
Fixed.Reset(mut self) ends self

// std.simd. Vec, Mask, Lane, CpuFeatures and Choice below are simd.Vec, simd.Mask, simd.Lane, simd.CpuFeatures and simd.Choice
Vec[T, N].Splat(x: T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Zero(): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Iota(): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].FromArray(a: [N]T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ToArray(self): [N]T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].FromSlice(xs: []T, i: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].FromSliceOr(xs: []T, i: int, fill: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].CopyTo(self, xs: mut []T, i: int) requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].CopyToMasked(self, xs: mut []T, i: int, m: Mask[T, N]) requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Gather[I: Lane](xs: []T, idx: Vec[I, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Lane(self, const i: int): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N
Vec[T, N].With(self, const i: int, x: T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N
Vec[T, N].Shuffle(self, b: Vec[T, N], const idx: [N]int): Vec[T, N] from self, b requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Reverse(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Broadcast(self, const i: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N
Vec[T, N].Rotate(self, const k: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires k >= 0 && k < N
Vec[T, N].Slide(self, prev: Vec[T, N], const k: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires k >= 0 && k <= N
Vec[T, N].InterleaveLo(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].InterleaveHi(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].DeinterleaveEven(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].DeinterleaveOdd(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceSum(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceSumWrap(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceSumWide(self): i64 requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceMin(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceMax(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceAnd(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceOr(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ReduceXor(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Min(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Max(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Abs(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].AbsWrap(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].AddSat(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].SubSat(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].MulHigh(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].DivTrunc(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ShlEach(self, k: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].ShrEach(self, k: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Fma(self, b: Vec[T, N], c: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Sqrt(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Floor(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Ceil(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].RoundEven(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].RoundToZero(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].WidenLo[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
Vec[T, N].WidenHi[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
Vec[T, N].MulWidenLo[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
Vec[T, N].MulWidenHi[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
Vec[T, N].NarrowSat[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M == 2 * N && M <= 64 && 2 * SizeOf[U]() == SizeOf[T]()
Vec[T, N].Cast[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Trunc[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].CastSat[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Reinterpret[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * SizeOf[U]() == N * SizeOf[T]() && M >= 2 && M <= 64
Mask[T, N].FromBits(b: u64): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Bits(self): u64 requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Any(self): bool requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].All(self): bool requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].CountSet(self): int requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].FirstSet(self): ?int requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Select(self, a: Vec[T, N], b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Cast[U: Lane](self): Mask[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
simd.Lookup16[const N: int](idx: Vec[u8, N], table: Vec[u8, 16]): Vec[u8, N] requires N == 16 || N == 32 || N == 64
simd.Lanes[T: Lane](): int
simd.Cpu(): CpuFeatures
CpuFeatures.Of(const names: str): CpuFeatures
CpuFeatures.Has(&self, const name: str): bool
CpuFeatures.HasAll(&self, other: &CpuFeatures): bool
simd.Dispatch[F: Plain](choices: []Choice[F], base: F): F
simd.Equal(a: []u8, b: []u8): bool
simd.Compare(a: []u8, b: []u8): int
simd.Dot[T: Lane](a: []T, b: []T): T
simd.Sum[T: Lane](xs: []T): T
interface Lane {
struct Choice[F] {
```
