// verdict: reject E0505
// note: twin of ARN-handle-self-ok: `*a into c` where c is a by-value Ctx holding the arena's reference beside `mut outer`; the store lands in outer, which is no allocator, so the arena's making loan (its Fixed's `&Alloc`) is stored there, and outer outlives the Fixed: E0505. Accepted, an unsafe body within its signature leaves outer holding the address of a dead Fixed's allocator
import std.mem

struct Outer {
    al: ?&Alloc
}

struct Ctx {
    ar: &Arena
    out: mut Outer
}

B(a: &Arena, c: Ctx) *a into c {
}

Bm(a: &Arena, c: mut Ctx) *a into c {
}

ByValue(sys: &System): OutOfMemory!void {
    var outer := Outer(al: none)
    if sys.args.Len() > 0 {
        var buf: [4096]u8 = [0; 4096]
        var fixed := mem.Fixed.New(mut buf[..])
        var ar := try Arena.NewIn(fixed.Alloc())
        B(&ar, Ctx(ar: &ar, out: mut outer)) // <- E0505
        ar.Free()
    }
    _ = outer
}

ByMut(sys: &System): OutOfMemory!void {
    var outer := Outer(al: none)
    if sys.args.Len() > 0 {
        var buf: [4096]u8 = [0; 4096]
        var fixed := mem.Fixed.New(mut buf[..])
        var ar := try Arena.NewIn(fixed.Alloc())
        var c := Ctx(ar: &ar, out: mut outer)
        Bm(&ar, mut c) // <- E0505
        ar.Free()
    }
    _ = outer
}

export Main(sys: &System): OutOfMemory!void {
    try ByValue(sys)
    try ByMut(sys)
}
