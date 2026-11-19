// libm and the libc-mode entry in system-libc mode: cos and hypot from
// libm.so.6 named in links, cos again through std.dl's Open, Symbol and
// Close, then the arguments, one environment variable and exit status 7,
// which the libc-mode entry must hand through intact.
import std.dl
import std.raw

extern cos(x: f64): f64
extern hypot(x: f64, y: f64): f64

// DlCos opens libm.so.6 at run time and calls its cos through the address.
DlCos(io: Io, x: f64): dl.Error!f64 {
    lib := try dl.Open(io, "libm.so.6", dl.Search.System)
    defer lib.Close(io)
    var r := -2.0
    if p := lib.Symbol("cos") {
        var w := u64(raw.Addr(p))
        unsafe "cos takes and returns an f64, and lib is open" {
            f := raw.Read(raw.PtrCast[u64, fn(f64): f64](raw.OfMut(mut w)))
            r = f(x)
        }
    }
    return r
}

export Main(sys: &System): WriteError!int {
    var c := 0.0
    var h := 0.0
    unsafe "libm's cos and hypot read only their arguments" {
        c = cos(0.5)
        h = hypot(3.0, 4.0)
    }
    try sys.Print("cos {c}\nhypot {h}\n", (c, h))
    d := DlCos(sys.io, 0.5) else e {
        try sys.Print("dl {e}\n", (e))
        return 3
    }
    try sys.Print("dl cos {d}\n", (d))
    for a in sys.args {
        try sys.Print("arg {a}\n", (a))
    }
    if v := sys.Env("FFI4_VAR") {
        try sys.Print("env {v}\n", (v))
    }
    return 7
}
