// The cookbook's "Load a library at run time" (doc/cookbook.md)
// with a Main that prints ScreenWidth(io): a positive width on
// x86_64-windows; on x86_64-linux outside system-libc mode Open fails with
// Unsupported, printed, and the exit status is 3.
import std.dl
import std.raw

ScreenWidth(io: Io): dl.Error!int {
    lib := try dl.Open(io, "user32.dll", dl.Search.System)
    defer lib.Close(io)
    var width := 0
    if p := lib.Symbol("GetSystemMetrics") {
        var w := u64(raw.Addr(p))
        unsafe "GetSystemMetrics takes and returns an i32, and lib is open" {
            f := raw.Read(raw.PtrCast[u64, fn(i32): i32](raw.OfMut(mut w)))
            width = int(f(0))
        }
    }
    return width
}

export Main(sys: &System): WriteError!int {
    width := ScreenWidth(sys.io) else e {
        try sys.Print("{e}\n", (e))
        return 3
    }
    try sys.Print("{width}\n", (width))
    return 0
}
