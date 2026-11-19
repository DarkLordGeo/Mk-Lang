// Why: the pipeline picks one set of kernels per CPU path at run time.
// What: the kernel tables v128, w256, w512, v256 and v512.
// How: a path fixes the lane counts and CPU features its bodies are compiled
// for; v256 and v512 carry cpu clauses, so only std.simd.Dispatch or a test of
// simd.Cpu() may pick them (src/dispatch). The v128 swap is the 16-lane
// Shuffle body; the v256 and v512 convolutions add by Fma.

import std.simd

/// The kernels of one path, as plain function values.
export struct Kernels {
    /// The path's name, as `--path` takes it
    export name: str
    /// `dst[i]` is `hi` where `src[i] >= t`, else 0
    export threshold: fn([]u8, mut []u8, u8, u8)
    /// `maxval - v`
    export invert: fn([]u8, mut []u8, u8)
    /// `v + delta`, clamped to 0..maxval
    export brighten: fn([]u8, mut []u8, i32, u8)
    /// `lut[v]`
    export lut: fn([]u8, mut []u8, &[256]u8)
    /// Rows of the convolution from `y0`
    export convolve: fn(&Frame, &Kernel, int, mut []u8, mut []f32)
    /// A row reversed pixel by pixel
    export mirror: fn([]u8, mut []u8, int)
    /// A colour row's luma
    export gray: fn([]u8, mut []u8)
    /// A row of the 2x2 average of two rows
    export half: fn([]u8, []u8, mut []u8, int)
    /// A row of the 2x enlargement
    export double: fn([]u8, mut []u8, int)
    /// `v * gain / 256`, clamped
    export gain: fn([]u8, mut []u8, u16, u8)
    /// A row of the gradient magnitude
    export sobel: fn(&Frame, int, mut []u8, int)
    /// A row of the bilinear resize
    export resize: fn(&Frame, int, int, mut []u8)
    /// A row of the 3x3 dilation or erosion of a gray image
    export morph: fn(&Frame, int, mut []u8, bool)
    /// A colour row with its channels reordered
    export swap: fn([]u8, mut []u8, int)
    /// Each sample with its low bits cleared
    export posterize: fn([]u8, mut []u8, &[3]u8, int)
    /// The 256-entry gamma table for the exponent 1 / g
    export gamma: fn(f32, int, mut []u8)
}

/// The scalar kernels, which define every result. O(1).
export Scalar(): Kernels {
    return Kernels(
        name: "scalar",
        threshold: ThresholdScalar,
        invert: InvertScalar,
        brighten: BrightenScalar,
        lut: LutScalar,
        convolve: ConvolveScalar,
        mirror: MirrorScalar,
        gray: GrayScalar,
        half: HalfScalar,
        double: DoubleScalar,
        gain: GainScalar,
        sobel: SobelScalar,
        resize: ResizeScalar,
        morph: MorphScalar,
        swap: SwapScalar,
        posterize: PosterizeScalar,
        gamma: GammaScalar,
    )
}

ThresholdV128(src: []u8, dst: mut []u8, t: u8, hi: u8) {
    ThresholdN[simd.Lanes[u8]()](src, dst, t, hi)
}

InvertV128(src: []u8, dst: mut []u8, maxval: u8) {
    InvertN[simd.Lanes[u8]()](src, dst, maxval)
}

BrightenV128(src: []u8, dst: mut []u8, delta: i32, maxval: u8) {
    BrightenN[simd.Lanes[u8]()](src, dst, delta, maxval)
}

LutV128(src: []u8, dst: mut []u8, lut: &[256]u8) {
    LutN[simd.Lanes[u8]()](src, dst, lut)
}

ConvolveV128(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) {
    ConvolveN[simd.Lanes[f32]()](f, k, y0, dst, scratch)
}

MirrorV128(src: []u8, dst: mut []u8, channels: int) {
    MirrorN[simd.Lanes[u8]()](src, dst, channels)
}

GrayV128(src: []u8, dst: mut []u8) {
    GrayN[simd.Lanes[u8](), 8](src, dst)
}

HalfV128(top: []u8, bottom: []u8, dst: mut []u8, channels: int) {
    HalfN[simd.Lanes[u8](), 8](top, bottom, dst, channels)
}

DoubleV128(src: []u8, dst: mut []u8, channels: int) {
    DoubleN[simd.Lanes[u8]()](src, dst, channels)
}

GainV128(src: []u8, dst: mut []u8, gain: u16, maxval: u8) {
    GainN[simd.Lanes[u8](), 8](src, dst, gain, maxval)
}

SobelV128(f: &Frame, y: int, dst: mut []u8, mode: int) {
    SobelN[simd.Lanes[f32]()](f, y, dst, mode)
}

ResizeV128(f: &Frame, y: int, out_height: int, dst: mut []u8) {
    ResizeN[simd.Lanes[f32]()](f, y, out_height, dst)
}

MorphV128(f: &Frame, y: int, dst: mut []u8, dilate: bool) {
    MorphN[simd.Lanes[u8]()](f, y, dst, dilate)
}

SwapV128(src: []u8, dst: mut []u8, order: int) {
    Swap16(src, dst, order)
}

PosterizeV128(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) {
    PosterizeN[simd.Lanes[u8]()](src, dst, shifts, channels)
}

GammaV128(inverse: f32, maxval: int, lut: mut []u8) {
    GammaN[simd.Lanes[f32]()](inverse, maxval, lut)
}

/// The kernels at the target's own lane counts, on the baseline CPU. O(1).
export V128(): Kernels {
    return Kernels(
        name: "v128",
        threshold: ThresholdV128,
        invert: InvertV128,
        brighten: BrightenV128,
        lut: LutV128,
        convolve: ConvolveV128,
        mirror: MirrorV128,
        gray: GrayV128,
        half: HalfV128,
        double: DoubleV128,
        gain: GainV128,
        sobel: SobelV128,
        resize: ResizeV128,
        morph: MorphV128,
        swap: SwapV128,
        posterize: PosterizeV128,
        gamma: GammaV128,
    )
}

ThresholdW256(src: []u8, dst: mut []u8, t: u8, hi: u8) {
    ThresholdN[32](src, dst, t, hi)
}

InvertW256(src: []u8, dst: mut []u8, maxval: u8) {
    InvertN[32](src, dst, maxval)
}

BrightenW256(src: []u8, dst: mut []u8, delta: i32, maxval: u8) {
    BrightenN[32](src, dst, delta, maxval)
}

LutW256(src: []u8, dst: mut []u8, lut: &[256]u8) {
    LutN[32](src, dst, lut)
}

ConvolveW256(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) {
    ConvolveN[8](f, k, y0, dst, scratch)
}

MirrorW256(src: []u8, dst: mut []u8, channels: int) {
    MirrorN[32](src, dst, channels)
}

GrayW256(src: []u8, dst: mut []u8) {
    GrayN[32, 16](src, dst)
}

HalfW256(top: []u8, bottom: []u8, dst: mut []u8, channels: int) {
    HalfN[32, 16](top, bottom, dst, channels)
}

DoubleW256(src: []u8, dst: mut []u8, channels: int) {
    DoubleN[32](src, dst, channels)
}

GainW256(src: []u8, dst: mut []u8, gain: u16, maxval: u8) {
    GainN[32, 16](src, dst, gain, maxval)
}

SobelW256(f: &Frame, y: int, dst: mut []u8, mode: int) {
    SobelN[8](f, y, dst, mode)
}

ResizeW256(f: &Frame, y: int, out_height: int, dst: mut []u8) {
    ResizeN[8](f, y, out_height, dst)
}

MorphW256(f: &Frame, y: int, dst: mut []u8, dilate: bool) {
    MorphN[32](f, y, dst, dilate)
}

SwapW256(src: []u8, dst: mut []u8, order: int) {
    SwapN[32](src, dst, order)
}

PosterizeW256(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) {
    PosterizeN[32](src, dst, shifts, channels)
}

GammaW256(inverse: f32, maxval: int, lut: mut []u8) {
    GammaN[8](inverse, maxval, lut)
}

/// The kernels at 256-bit lane counts on the baseline CPU. O(1).
export W256(): Kernels {
    return Kernels(
        name: "w256",
        threshold: ThresholdW256,
        invert: InvertW256,
        brighten: BrightenW256,
        lut: LutW256,
        convolve: ConvolveW256,
        mirror: MirrorW256,
        gray: GrayW256,
        half: HalfW256,
        double: DoubleW256,
        gain: GainW256,
        sobel: SobelW256,
        resize: ResizeW256,
        morph: MorphW256,
        swap: SwapW256,
        posterize: PosterizeW256,
        gamma: GammaW256,
    )
}

ThresholdW512(src: []u8, dst: mut []u8, t: u8, hi: u8) {
    ThresholdN[64](src, dst, t, hi)
}

InvertW512(src: []u8, dst: mut []u8, maxval: u8) {
    InvertN[64](src, dst, maxval)
}

BrightenW512(src: []u8, dst: mut []u8, delta: i32, maxval: u8) {
    BrightenN[64](src, dst, delta, maxval)
}

LutW512(src: []u8, dst: mut []u8, lut: &[256]u8) {
    LutN[64](src, dst, lut)
}

ConvolveW512(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) {
    ConvolveN[16](f, k, y0, dst, scratch)
}

MirrorW512(src: []u8, dst: mut []u8, channels: int) {
    MirrorN[64](src, dst, channels)
}

GrayW512(src: []u8, dst: mut []u8) {
    GrayN[64, 32](src, dst)
}

HalfW512(top: []u8, bottom: []u8, dst: mut []u8, channels: int) {
    HalfN[64, 32](top, bottom, dst, channels)
}

DoubleW512(src: []u8, dst: mut []u8, channels: int) {
    DoubleN[64](src, dst, channels)
}

GainW512(src: []u8, dst: mut []u8, gain: u16, maxval: u8) {
    GainN[64, 32](src, dst, gain, maxval)
}

SobelW512(f: &Frame, y: int, dst: mut []u8, mode: int) {
    SobelN[16](f, y, dst, mode)
}

ResizeW512(f: &Frame, y: int, out_height: int, dst: mut []u8) {
    ResizeN[16](f, y, out_height, dst)
}

MorphW512(f: &Frame, y: int, dst: mut []u8, dilate: bool) {
    MorphN[64](f, y, dst, dilate)
}

SwapW512(src: []u8, dst: mut []u8, order: int) {
    SwapN[64](src, dst, order)
}

PosterizeW512(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) {
    PosterizeN[64](src, dst, shifts, channels)
}

GammaW512(inverse: f32, maxval: int, lut: mut []u8) {
    GammaN[16](inverse, maxval, lut)
}

/// The kernels at 512-bit lane counts on the baseline CPU. O(1).
export W512(): Kernels {
    return Kernels(
        name: "w512",
        threshold: ThresholdW512,
        invert: InvertW512,
        brighten: BrightenW512,
        lut: LutW512,
        convolve: ConvolveW512,
        mirror: MirrorW512,
        gray: GrayW512,
        half: HalfW512,
        double: DoubleW512,
        gain: GainW512,
        sobel: SobelW512,
        resize: ResizeW512,
        morph: MorphW512,
        swap: SwapW512,
        posterize: PosterizeW512,
        gamma: GammaW512,
    )
}

/// The threshold for CPUs with AVX2 and FMA (x86-64-v3).
export ThresholdV256(src: []u8, dst: mut []u8, t: u8, hi: u8) cpu "x86-64-v3" {
    ThresholdN[32](src, dst, t, hi)
}

/// The inversion for CPUs with AVX2 and FMA (x86-64-v3).
export InvertV256(src: []u8, dst: mut []u8, maxval: u8) cpu "x86-64-v3" {
    InvertN[32](src, dst, maxval)
}

/// The brightness change for CPUs with AVX2 and FMA (x86-64-v3).
export BrightenV256(src: []u8, dst: mut []u8, delta: i32, maxval: u8) cpu "x86-64-v3" {
    BrightenN[32](src, dst, delta, maxval)
}

/// The table lookup for CPUs with AVX2 and FMA (x86-64-v3).
export LutV256(src: []u8, dst: mut []u8, lut: &[256]u8) cpu "x86-64-v3" {
    LutN[32](src, dst, lut)
}

/// The convolution for CPUs with AVX2 and FMA (x86-64-v3).
export ConvolveV256(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) cpu "x86-64-v3" {
    ConvolveFusedN[8](f, k, y0, dst, scratch)
}

/// The mirror for CPUs with AVX2 and FMA (x86-64-v3).
export MirrorV256(src: []u8, dst: mut []u8, channels: int) cpu "x86-64-v3" {
    MirrorN[32](src, dst, channels)
}

/// The luma for CPUs with AVX2 and FMA (x86-64-v3).
export GrayV256(src: []u8, dst: mut []u8) cpu "x86-64-v3" {
    GrayN[32, 16](src, dst)
}

/// The 2x2 average for CPUs with AVX2 and FMA (x86-64-v3).
export HalfV256(top: []u8, bottom: []u8, dst: mut []u8, channels: int) cpu "x86-64-v3" {
    HalfN[32, 16](top, bottom, dst, channels)
}

/// The enlargement for CPUs with AVX2 and FMA (x86-64-v3).
export DoubleV256(src: []u8, dst: mut []u8, channels: int) cpu "x86-64-v3" {
    DoubleN[32](src, dst, channels)
}

/// The gain for CPUs with AVX2 and FMA (x86-64-v3).
export GainV256(src: []u8, dst: mut []u8, gain: u16, maxval: u8) cpu "x86-64-v3" {
    GainN[32, 16](src, dst, gain, maxval)
}

/// The Sobel gradient for CPUs with AVX2 and FMA (x86-64-v3).
export SobelV256(f: &Frame, y: int, dst: mut []u8, mode: int) cpu "x86-64-v3" {
    SobelN[8](f, y, dst, mode)
}

/// The bilinear resize for CPUs with AVX2 and FMA (x86-64-v3).
export ResizeV256(f: &Frame, y: int, out_height: int, dst: mut []u8) cpu "x86-64-v3" {
    ResizeN[8](f, y, out_height, dst)
}

/// Dilation and erosion for CPUs with AVX2 and FMA (x86-64-v3).
export MorphV256(f: &Frame, y: int, dst: mut []u8, dilate: bool) cpu "x86-64-v3" {
    MorphN[32](f, y, dst, dilate)
}

/// The channel reorder for CPUs with AVX2 and FMA (x86-64-v3).
export SwapV256(src: []u8, dst: mut []u8, order: int) cpu "x86-64-v3" {
    SwapN[32](src, dst, order)
}

/// The posterization for CPUs with AVX2 and FMA (x86-64-v3).
export PosterizeV256(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) cpu "x86-64-v3" {
    PosterizeN[32](src, dst, shifts, channels)
}

/// The gamma table for CPUs with AVX2 and FMA (x86-64-v3).
export GammaV256(inverse: f32, maxval: int, lut: mut []u8) cpu "x86-64-v3" {
    GammaN[8](inverse, maxval, lut)
}

/// The threshold for CPUs with AVX-512 (x86-64-v4).
export ThresholdV512(src: []u8, dst: mut []u8, t: u8, hi: u8) cpu "x86-64-v4" {
    ThresholdN[64](src, dst, t, hi)
}

/// The inversion for CPUs with AVX-512 (x86-64-v4).
export InvertV512(src: []u8, dst: mut []u8, maxval: u8) cpu "x86-64-v4" {
    InvertN[64](src, dst, maxval)
}

/// The brightness change for CPUs with AVX-512 (x86-64-v4).
export BrightenV512(src: []u8, dst: mut []u8, delta: i32, maxval: u8) cpu "x86-64-v4" {
    BrightenN[64](src, dst, delta, maxval)
}

/// The table lookup for CPUs with AVX-512 (x86-64-v4).
export LutV512(src: []u8, dst: mut []u8, lut: &[256]u8) cpu "x86-64-v4" {
    LutN[64](src, dst, lut)
}

/// The convolution for CPUs with AVX-512 (x86-64-v4).
export ConvolveV512(f: &Frame, k: &Kernel, y0: int, dst: mut []u8, scratch: mut []f32) cpu "x86-64-v4" {
    ConvolveFusedN[16](f, k, y0, dst, scratch)
}

/// The mirror for CPUs with AVX-512 (x86-64-v4).
export MirrorV512(src: []u8, dst: mut []u8, channels: int) cpu "x86-64-v4" {
    MirrorN[64](src, dst, channels)
}

/// The luma for CPUs with AVX-512 (x86-64-v4).
export GrayV512(src: []u8, dst: mut []u8) cpu "x86-64-v4" {
    GrayN[64, 32](src, dst)
}

/// The 2x2 average for CPUs with AVX-512 (x86-64-v4).
export HalfV512(top: []u8, bottom: []u8, dst: mut []u8, channels: int) cpu "x86-64-v4" {
    HalfN[64, 32](top, bottom, dst, channels)
}

/// The enlargement for CPUs with AVX-512 (x86-64-v4).
export DoubleV512(src: []u8, dst: mut []u8, channels: int) cpu "x86-64-v4" {
    DoubleN[64](src, dst, channels)
}

/// The gain for CPUs with AVX-512 (x86-64-v4).
export GainV512(src: []u8, dst: mut []u8, gain: u16, maxval: u8) cpu "x86-64-v4" {
    GainN[64, 32](src, dst, gain, maxval)
}

/// The Sobel gradient for CPUs with AVX-512 (x86-64-v4).
export SobelV512(f: &Frame, y: int, dst: mut []u8, mode: int) cpu "x86-64-v4" {
    SobelN[16](f, y, dst, mode)
}

/// The bilinear resize for CPUs with AVX-512 (x86-64-v4).
export ResizeV512(f: &Frame, y: int, out_height: int, dst: mut []u8) cpu "x86-64-v4" {
    ResizeN[16](f, y, out_height, dst)
}

/// Dilation and erosion for CPUs with AVX-512 (x86-64-v4).
export MorphV512(f: &Frame, y: int, dst: mut []u8, dilate: bool) cpu "x86-64-v4" {
    MorphN[64](f, y, dst, dilate)
}

/// The channel reorder for CPUs with AVX-512 (x86-64-v4).
export SwapV512(src: []u8, dst: mut []u8, order: int) cpu "x86-64-v4" {
    SwapN[64](src, dst, order)
}

/// The posterization for CPUs with AVX-512 (x86-64-v4).
export PosterizeV512(src: []u8, dst: mut []u8, shifts: &[3]u8, channels: int) cpu "x86-64-v4" {
    PosterizeN[64](src, dst, shifts, channels)
}

/// The gamma table for CPUs with AVX-512 (x86-64-v4).
export GammaV512(inverse: f32, maxval: int, lut: mut []u8) cpu "x86-64-v4" {
    GammaN[16](inverse, maxval, lut)
}
