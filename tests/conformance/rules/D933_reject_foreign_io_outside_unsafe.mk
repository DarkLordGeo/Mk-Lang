// verdict: reject E1101
// rule: D-933
// note: runtime.ForeignIo is unsafe only: it makes an Io from a raw context pointer and a table of entries; D933_accept_foreign_io_in_unsafe makes the same Foreign in an unsafe block of a listed module
import std.runtime

Make(ctx: *u8): Io {
    f := runtime.ForeignIo(ctx, &runtime.InertTable) // <- E1101
    io := f.Io()
    f.Release()
    return io
}
