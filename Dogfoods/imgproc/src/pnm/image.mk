// Why: one image type for the decoder, the pipeline and the encoder.
// What: Image, with New, Like, Samples, SamplesMut and RowBytes.
// How: 8-bit samples row by row, a pixel's channels side by side.

/// An image of 8-bit samples stored row by row, the channels of a pixel side
/// by side, as PGM (one channel) and PPM (three channels) store them.
export linear view struct Image {
    export width: int
    export height: int
    /// 1 for gray, 3 for red, green and blue
    export channels: int
    /// The largest sample value, from 1 to 255
    export maxval: int
    pixels: HeapList[u8]
}

/// The largest width or height accepted, so that every sample index fits
/// comfortably in an i32 lane.
export const MaxSide: int = 16384

/// The largest number of samples in one image (64 MiB).
export const MaxSamples: int = 67108864

/// Makes an image whose samples are all 0. Fails with `OutOfMemory` when `a`
/// cannot hold width * height * channels bytes. O(n); allocates n bytes from `a`.
export Image.New(a: &Alloc, width: int, height: int, channels: int, maxval: int): OutOfMemory!Image
    requires width > 0 && height > 0 && (channels == 1 || channels == 3)
{
    var px := HeapList[u8].New(a)
    onerr px.Free(a)
    n := width * height * channels
    for i in 0..n {
        _ = i
        try px.Push(a, 0)
    }
    return Image(width: width, height: height, channels: channels, maxval: maxval, pixels: px)
}

/// Makes an image with the shape of `self` and samples all 0. Fails with
/// `OutOfMemory` as `New` does. O(n); allocates n bytes from `a`.
export Image.Like(&self, a: &Alloc): OutOfMemory!Image from a {
    return try Image.New(a, self.width, self.height, self.channels, self.maxval)
}

/// The samples, row by row. O(1).
export Image.Samples(&self): []u8 {
    return self.pixels.Items()
}

/// The samples, for writing. O(1).
export Image.SamplesMut(mut self): mut []u8 {
    return self.pixels.ItemsMut()
}

/// The number of bytes in one row. O(1).
export Image.RowBytes(&self): int {
    return self.width * self.channels
}

/// The PNM magic of the binary form of this image: `P5` or `P6`. O(1).
export Image.Magic(&self): str {
    return if self.channels == 1 { "P5" } else { "P6" }
}

impl Free for Image {
    Free(self, a: &Alloc) {
        Image(pixels, ..) := self
        pixels.Free(a)
    }
}
