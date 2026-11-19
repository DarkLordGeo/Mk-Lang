// verdict: ok
// rule: D-542
// note: doc/cookbook.md's "Load a library at run time" with the pkg.mk its std.raw import needs
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
