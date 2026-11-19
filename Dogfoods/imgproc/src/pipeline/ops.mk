// Why: operations are spelled on the command line.
// What: Op, OpKind, ParseOp, MakeKernel and the parse errors.
// How: each word becomes an Op with its parameters checked, as listed below.

import src.kernel
import src.pnm
import std.math
import std.simd

// The pipeline's operations as the command line spells them:
//
//   invert            maxval - v
//   brighten=D        v + D, clamped, D from -255 to 255
//   threshold[=T]     maxval where v >= T, else 0; Otsu's T when none is given
//   equalize          histogram equalisation over all channels together
//   levels            stretch the darkest sample to 0 and the brightest to maxval
//   blur, sharpen, edge, emboss, box=N
//   conv=W:W:...[/D][+B or -B]   a square kernel of integer weights
//   mirror, flip      reverse the columns, or the rows
//   gray              a colour image's luma, ITU-R BT.601 weights
//   half, double      the 2x2 average, or each pixel four times
//   gain=F            v * F, clamped, F from 1/256 to 255
//   sobel[=l1|max]    the gradient magnitude: Euclidean, sum or largest
//   resize=WxH        bilinear, a side of 0 keeping the aspect ratio
//   dilate, erode     the 3x3 largest or smallest, gray images only
//   swap=bgr|gbr|brg  reorder a colour image's channels
//   posterize=B[:G:B] keep the top B bits of each channel, 1 to 8
//   gamma=G           maxval * (v / maxval)^(1 / G), G from 0.1 to 10
//
// A kernel's weights must keep every sum exact in f32 (see kernel.Frame), so
// the sum of their magnitudes times 255 stays below 2^24.

/// What an operation does.
export enum OpKind {
    Invert
    Brighten
    Threshold
    Equalize
    Levels
    Convolve
    Mirror
    Flip
    Gray
    Half
    Double
    Gain
    Sobel
    Resize
    Dilate
    Erode
    Swap
    Posterize
    Gamma
}

/// One parsed operation. `amount` is the brightness delta, the threshold or
/// -1 for an automatic one, the gain or the gamma in thousandths, the Sobel
/// combination or the channel order; `filter` is the convolution kernel;
/// `args` holds resize's width and height and posterize's shift per channel.
export struct Op {
    export kind: OpKind
    export amount: int
    export filter: kernel.Kernel
    export args: [3]int
}

/// No operation has this name.
export error UnknownOp
/// The operation's argument is missing, malformed or out of range.
export error BadArgument
/// A kernel's weights are not a square number, its side is even or above 15.
export error BadKernel
/// A kernel's weights could make a sum f32 cannot hold exactly.
export error HeavyKernel

export type OpError = UnknownOp | BadArgument | BadKernel | HeavyKernel | OutOfMemory

/// The largest sum of weight magnitudes: 255 times it stays below 2^24.
const MaxWeight: f64 = 65793.0

/// Parses an integer written in decimal with an optional sign.
ParseWhole(text: str): BadArgument!int {
    n := text.ParseInt() else {
        fail BadArgument
    }
    if n < -1_000_000_000 || n > 1_000_000_000 {
        fail BadArgument
    }
    return Trunc[int](n)
}

/// A kernel from integer weights, row by row, with its divisor and bias.
/// A divisor of 0 means the sum of the weights, or 1 when that is 0. Fails
/// with `BadKernel` unless the count is an odd square of side up to 15, and
/// `HeavyKernel` when a sum could leave f32's exact integers. O(n);
/// allocates n floats in `a`.
export MakeKernel(a: &Arena, weights: []int, divisor: int, bias: int): BadKernel|HeavyKernel|OutOfMemory!kernel.Kernel from a {
    side := Cast[int](math.Sqrt(Cast[f64](weights.Len())))
    if side * side != weights.Len() || side % 2 == 0 || side > 15 {
        fail BadKernel
    }
    w := try a.Slice[f32](weights.Len())
    var heavy: f64 = 0.0
    for i, x in weights {
        heavy += math.Abs(Cast[f64](x))
        w[i] = Cast[f32](x)
    }
    if heavy > MaxWeight {
        fail HeavyKernel
    }
    var d := Cast[f32](divisor)
    if divisor == 0 {
        d = simd.Sum(w[..])
        if d == 0.0 {
            d = 1.0
        }
    }
    return kernel.Kernel(size: side, weights: w[..], divisor: d, bias: Cast[f32](bias))
}

/// The weights after `conv=`, separated by `:`, then `/D` and `+B` or `-B`.
ParseConv(a: &Arena, spec: str): OpError!kernel.Kernel from a {
    var body := spec
    var bias := 0
    if at := body.Find("+") {
        bias = try ParseWhole(body[at + 1..])
        body = body[..at]
    } else if at := LastMinus(body) {
        bias = try ParseWhole(body[at..])
        body = body[..at]
    }
    var divisor := 0
    if at := body.Find("/") {
        divisor = try ParseWhole(body[at + 1..])
        if divisor == 0 {
            fail BadArgument
        }
        body = body[..at]
    }
    var ws := List[int].New(a)
    for part in body.Split(":") {
        try ws.Push(try ParseWhole(part))
    }
    return try MakeKernel(a, ws.Done()[..], divisor, bias)
}

/// The offset of a `-` that starts a bias: one that follows a digit.
LastMinus(s: str): ?int {
    var i := s.Len() - 1
    for i > 0 {
        if s.Byte(i) == b'-' && s.Byte(i - 1) >= b'0' && s.Byte(i - 1) <= b'9' {
            return some(i)
        }
        i -= 1
    }
    return none
}

/// A named kernel.
Preset(a: &Arena, name: str): OpError!?kernel.Kernel from a {
    if name == "blur" {
        return some(try MakeKernel(a, [1, 2, 1, 2, 4, 2, 1, 2, 1][..], 16, 0))
    }
    if name == "sharpen" {
        return some(try MakeKernel(a, [0, -1, 0, -1, 5, -1, 0, -1, 0][..], 1, 0))
    }
    if name == "edge" {
        return some(try MakeKernel(a, [-1, -1, -1, -1, 8, -1, -1, -1, -1][..], 1, 0))
    }
    if name == "emboss" {
        return some(try MakeKernel(a, [-2, -1, 0, -1, 1, 1, 0, 1, 2][..], 1, 128))
    }
    return none
}

/// An identity kernel for the operations that need none.
Identity(a: &Arena): OutOfMemory!kernel.Kernel from a {
    w := try a.Slice[f32](1)
    w[0] = 1.0
    return kernel.Kernel(size: 1, weights: w[..], divisor: 1.0, bias: 0.0)
}

/// Parses one operation. O(n) in its text; allocates a kernel in `a`.
export ParseOp(a: &Arena, text: str): OpError!Op from a {
    var name := text
    var arg := ""
    if eq := text.Find("=") {
        name = text[..eq]
        arg = text[eq + 1..]
    }
    if k := try Preset(a, name) {
        return Op(kind: OpKind.Convolve, amount: 0, filter: k, args: [0, 0, 0])
    }
    if name == "conv" {
        return Op(kind: OpKind.Convolve, amount: 0, filter: try ParseConv(a, arg), args: [0, 0, 0])
    }
    if name == "box" {
        side := try ParseWhole(arg)
        if side < 1 || side > 15 || side % 2 == 0 {
            fail BadKernel
        }
        var ws := List[int].New(a)
        for i in 0..side * side {
            _ = i
            try ws.Push(1)
        }
        return Op(kind: OpKind.Convolve, amount: 0, filter: try MakeKernel(a, ws.Done()[..], 0, 0), args: [0, 0, 0])
    }
    plain := try Identity(a)
    if shape := Geometry(name) {
        if arg != "" {
            fail BadArgument
        }
        return Op(kind: shape, amount: 0, filter: plain, args: [0, 0, 0])
    }
    if name == "sobel" {
        mode := if arg == "" {
            kernel.SobelL2
        } else if arg == "l1" {
            kernel.SobelL1
        } else if arg == "max" {
            kernel.SobelMax
        } else {
            fail BadArgument
        }
        return Op(kind: OpKind.Sobel, amount: mode, filter: plain, args: [0, 0, 0])
    }
    if name == "gain" {
        f := arg.ParseFloat() else {
            fail BadArgument
        }
        if !(f >= 1.0 / 256.0 && f < 256.0) {
            fail BadArgument
        }
        return Op(kind: OpKind.Gain, amount: Cast[int](f * 256.0), filter: plain, args: [0, 0, 0])
    }
    if name == "invert" || name == "equalize" || name == "levels" {
        if arg != "" {
            fail BadArgument
        }
        kind := if name == "invert" {
            OpKind.Invert
        } else if name == "equalize" {
            OpKind.Equalize
        } else {
            OpKind.Levels
        }
        return Op(kind: kind, amount: 0, filter: plain, args: [0, 0, 0])
    }
    if name == "brighten" {
        d := try ParseWhole(arg)
        if d < -255 || d > 255 {
            fail BadArgument
        }
        return Op(kind: OpKind.Brighten, amount: d, filter: plain, args: [0, 0, 0])
    }
    if name == "threshold" {
        if arg == "" {
            return Op(kind: OpKind.Threshold, amount: -1, filter: plain, args: [0, 0, 0])
        }
        t := try ParseWhole(arg)
        if t < 0 || t > 255 {
            fail BadArgument
        }
        return Op(kind: OpKind.Threshold, amount: t, filter: plain, args: [0, 0, 0])
    }
    if name == "resize" {
        return Op(kind: OpKind.Resize, amount: 0, filter: plain, args: try ParseSize(arg))
    }
    if name == "posterize" {
        return Op(kind: OpKind.Posterize, amount: 0, filter: plain, args: try ParseBits(arg))
    }
    if name == "swap" {
        order := if arg == "bgr" {
            kernel.OrderBgr
        } else if arg == "gbr" {
            kernel.OrderGbr
        } else if arg == "brg" {
            kernel.OrderBrg
        } else {
            fail BadArgument
        }
        return Op(kind: OpKind.Swap, amount: order, filter: plain, args: [0, 0, 0])
    }
    if name == "gamma" {
        g := arg.ParseFloat() else {
            fail BadArgument
        }
        if !(g >= 0.1 && g <= 10.0) {
            fail BadArgument
        }
        return Op(kind: OpKind.Gamma, amount: Cast[int](g * 1000.0 + 0.5), filter: plain, args: [0, 0, 0])
    }
    fail UnknownOp
}

/// The width and height after `resize=`, as `WxH`, each up to MaxSide and
/// not both 0.
export ParseSize(text: str): BadArgument![3]int {
    at := text.Find("x") else {
        fail BadArgument
    }
    w := try ParseWhole(text[..at])
    h := try ParseWhole(text[at + 1..])
    if w < 0 || h < 0 || w > pnm.MaxSide || h > pnm.MaxSide || w + h == 0 {
        fail BadArgument
    }
    return [w, h, 0]
}

/// The bits kept per channel after `posterize=`, one count for every
/// channel or three separated by `:`, each from 1 to 8, as the shift that
/// clears the rest.
ParseBits(text: str): BadArgument![3]int {
    var shifts: [3]int = [0; 3]
    var n := 0
    for part in text.Split(":") {
        if n == 3 {
            fail BadArgument
        }
        bits := try ParseWhole(part)
        if bits < 1 || bits > 8 {
            fail BadArgument
        }
        shifts[n] = 8 - bits
        n += 1
    }
    if n == 1 {
        return [shifts[0], shifts[0], shifts[0]]
    }
    if n != 3 {
        fail BadArgument
    }
    return shifts
}

/// Why `op` cannot run on an image of `channels` channels, or `none`.
export Unsupported(op: &Op, channels: int): ?str {
    if (op.kind == OpKind.Dilate || op.kind == OpKind.Erode) && channels != 1 {
        return some("dilate and erode work on gray images; put gray before them")
    }
    if op.kind == OpKind.Swap && channels != 3 {
        return some("swap needs a colour image")
    }
    if op.kind == OpKind.Posterize && channels != 3 && (op.args[1] != op.args[0] || op.args[2] != op.args[0]) {
        return some("posterize with a count per channel needs a colour image")
    }
    return none
}

/// The operations named by a word alone that move or combine pixels.
Geometry(name: str): ?OpKind {
    if name == "mirror" {
        return some(OpKind.Mirror)
    }
    if name == "flip" {
        return some(OpKind.Flip)
    }
    if name == "gray" {
        return some(OpKind.Gray)
    }
    if name == "half" {
        return some(OpKind.Half)
    }
    if name == "double" {
        return some(OpKind.Double)
    }
    if name == "dilate" {
        return some(OpKind.Dilate)
    }
    if name == "erode" {
        return some(OpKind.Erode)
    }
    return none
}

/// The shape of the image `op` makes from one of `width` by `height` pixels
/// of `channels` samples. O(1).
export OutShape(op: &Op, width: int, height: int, channels: int): (width: int, height: int, channels: int) {
    return match op.kind {
        .Gray { (width: width, height: height, channels: 1) }
        .Half { (width: (width + 1) / 2, height: (height + 1) / 2, channels: channels) }
        .Double { (width: 2 * width, height: 2 * height, channels: channels) }
        .Resize { Resized(op.args[0], op.args[1], width, height, channels) }
        _ { (width: width, height: height, channels: channels) }
    }
}

/// The shape `resize=WxH` makes from `width` by `height`: a side of 0 keeps
/// the aspect ratio, rounded to the nearest pixel and at least 1. O(1).
Resized(w: int, h: int, width: int, height: int, channels: int): (width: int, height: int, channels: int) {
    if w == 0 {
        return (width: math.Max((width * h + height / 2) / height, 1), height: h, channels: channels)
    }
    if h == 0 {
        return (width: w, height: math.Max((height * w + width / 2) / width, 1), channels: channels)
    }
    return (width: w, height: h, channels: channels)
}
