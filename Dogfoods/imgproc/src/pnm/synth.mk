// Why: timing a pipeline needs an input that is the same everywhere.
// What: Synthetic, the test pattern `synth:WxH` names.
// How: gradients in red and green, rings in blue, and noise from a math.Rng
// with a fixed seed.

import std.math

/// A `width` by `height` colour test pattern with maxval 255. Fails with
/// `OutOfMemory` when `a` cannot hold it. O(n); allocates n bytes from `a`.
export Synthetic(a: &Alloc, width: int, height: int): OutOfMemory!Image
    requires width > 0 && height > 0
{
    var img := try Image.New(a, width, height, 3, 255)
    var rng := math.Rng.New(1)
    px := img.SamplesMut()
    cx := width / 2
    cy := height / 2
    for y in 0..height {
        for x in 0..width {
            i := (y * width + x) * 3
            noise := Cast[int](rng.Below(33)) - 16
            d := (x - cx) * (x - cx) + (y - cy) * (y - cy)
            ring := Cast[int](math.Sqrt(Cast[f64](d))) * 8 % 256
            px[i] = Trunc[u8](math.Min(math.Max((x * 255 / width + y * 255 / height) / 2 + noise, 0), 255))
            px[i + 1] = Trunc[u8](y * 255 / height)
            px[i + 2] = Trunc[u8](if ring < 128 { ring * 2 } else { 511 - ring * 2 })
        }
    }
    return img
}
