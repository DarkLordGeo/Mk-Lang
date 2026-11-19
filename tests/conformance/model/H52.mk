// verdict: reject E0503
// case: H52
// note: the function's error set adds `WriteError`, the error of `Writer.Flush`, which `fs.Error` does not include; the model's text lacks it, which is E0801 before the E0503
import std.fs

Bad(io: Io, buf: mut []u8): fs.Error|WriteError!void {
    var f := try fs.Create(io, "out")
    onerr f.Abandon()
    var w := f.Writer(io, buf)
    try f.Close(io) // <- E0503
    try w.Flush()
}
