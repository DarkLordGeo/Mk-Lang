// Why: every kernel reads the same shapes.
// What: Frame, Kernel, ConvolveAt and RoundSample.
// How: edges are clamped; the byte-identity rule below holds for all paths.

import std.math
import std.simd

// The shapes every kernel reads: a frame of samples and a convolution kernel.
//
// Byte identity across paths: every kernel computes each output sample from
// the same operations in the same order whatever the lane count, and the
// convolution sums products of integers below 2^24, which f32 holds exactly,
// so no lane order, partial sum or fused multiply-add can change a byte.

/// A read-only view of an image's samples and shape.
export struct Frame {
    export samples: []u8
    export width: int
    export height: int
    /// 1 or 3
    export channels: int
    export maxval: int
}

/// A square convolution kernel of integer weights, applied as
/// `sum(w * p) / divisor + bias`, then clamped to 0..maxval and rounded half
/// to even.
export struct Kernel {
    /// The side, odd, from 1 to 15
    export size: int
    /// `size * size` integer weights, row by row
    export weights: []f32
    export divisor: f32
    export bias: f32
}

/// The bytes in one row of `f`. O(1).
export Frame.Row(&self): int {
    return self.width * self.channels
}

/// The row index `y` moved inside the frame, repeating the edge rows. O(1).
export Frame.ClampY(&self, y: int): int {
    return math.Min(math.Max(y, 0), self.height - 1)
}

/// The column index `x` moved inside the frame, repeating the edge columns.
/// O(1).
export Frame.ClampX(&self, x: int): int {
    return math.Min(math.Max(x, 0), self.width - 1)
}

/// Rounds `q`, which lies in 0..2^22, half to even, through the bits of
/// `q + 1.5 * 2^23`: in [2^23, 2^24) the f32 spacing is 1, so the add rounds
/// to an integer and the low mantissa bits hold it. O(1).
export RoundSample(q: f32): u8 {
    magic: f32 = 12582912.0
    return Trunc[u8]((q + magic).Bits() - 0x4B400000)
}

/// One output sample of the convolution at row `y`, byte `i` of the row,
/// with edge rows and columns repeated. `scratch` holds at least `k.size`
/// values. Each kernel row is a `simd.Dot`, which adds exactly here because
/// every product and partial sum is an integer below 2^24. O(size^2).
export ConvolveAt(f: &Frame, k: &Kernel, y: int, i: int, scratch: mut []f32): u8 {
    x := i / f.channels
    c := i % f.channels
    r := k.size / 2
    row := f.Row()
    var acc: f32 = 0.0
    for ky in 0..k.size {
        base := f.ClampY(y + ky - r) * row + c
        for kx in 0..k.size {
            scratch[kx] = f32(f.samples[base + f.ClampX(x + kx - r) * f.channels])
        }
        acc += simd.Dot(scratch[..k.size], k.weights[ky * k.size..(ky + 1) * k.size])
    }
    q := acc / k.divisor + k.bias
    return RoundSample(math.Min(math.Max(q, 0.0), Cast[f32](f.maxval)))
}
