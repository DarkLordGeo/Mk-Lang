// Static data aligned over a page sits at its
// alignment. Const slices of align(n) types made by Arena.Slice in an
// evaluation, plain consts and vars of such types, with odd-sized consts
// between them; each line is an address mod its alignment, where any
// non-zero value is misaligned static data. Each address is offset by the
// argument count less one, 0 when the program gets no argument, so release
// cannot fold the remainder from the global's declared alignment. On
// x86_64 Windows a plain layout puts most of them off their alignment in
// both profiles.

import std.raw

align(8192) struct L8K {
    a: u64
}

align(16384) struct L16K {
    a: u64
}

align(65536) struct L64K {
    a: u64
}

Mod(p: uint, al: uint): uint {
    return p % al
}

Sl8(a: &Arena): []L8K from a {
    s := a.Slice[L8K](2) else {
        Panic("oom")
    }
    s[1].a = 8
    return s[..]
}

Sl16(a: &Arena): []L16K from a {
    s := a.Slice[L16K](2) else {
        Panic("oom")
    }
    s[1].a = 16
    return s[..]
}

Sl64(a: &Arena, k: u64): []L64K from a {
    s := a.Slice[L64K](2) else {
        Panic("oom")
    }
    s[1].a = k
    return s[..]
}

Bytes(a: &Arena, n: int): []u8 from a {
    s := a.Slice[u8](n) else {
        Panic("oom")
    }
    return s[..]
}

const P1 = Bytes(EvalArena(), 3)
const S8 = Sl8(EvalArena())
const P2 = Bytes(EvalArena(), 5)
const S16 = Sl16(EvalArena())
const P3 = Bytes(EvalArena(), 7)
const S64 = Sl64(EvalArena(), 64)
const T64 = Sl64(EvalArena(), 65)
const C8 = L8K(a: 8)
const C64 = L64K(a: 64)

var V8: L8K = L8K(a: 1)
var V16: L16K = L16K(a: 2)
var V64: L64K = L64K(a: 3)
var Z64: L64K = L64K(a: 0)
// R8 is only read and W8 is written, beside each other in .mkalw$d: codegen
// pins both in @llvm.compiler.used, so release cannot make R8 a constant
// that gives their section read-only flags.
var R8: L8K = L8K(a: 5)
var W8: L8K = L8K(a: 0)

export Main(sys: &System): OutOfMemory|WriteError!void {
    var w := sys.Stdout()
    d := Trunc[uint](sys.args.Len() - 1)
    var at: [10]uint = [0; 10]
    unsafe "the addresses of static data, read as integers" {
        at[0] = raw.Addr(raw.Data(S8)) +% d
        at[1] = raw.Addr(raw.Data(S16)) +% d
        at[2] = raw.Addr(raw.Data(S64)) +% d
        at[3] = raw.Addr(raw.Data(T64)) +% d
        at[4] = raw.Addr(raw.Of(&C8)) +% d
        at[5] = raw.Addr(raw.Of(&C64)) +% d
        at[6] = raw.Addr(raw.Of(&V8)) +% d
        at[7] = raw.Addr(raw.Of(&V16)) +% d
        at[8] = raw.Addr(raw.Of(&V64)) +% d
        at[9] = raw.Addr(raw.Of(&Z64)) +% d
    }
    al: [10]uint = [8192, 16384, 65536, 65536, 8192, 65536, 8192, 16384, 65536, 65536]
    for i in 0..10 {
        try Print(mut w, "{i} %{a}={m}\n", (i: i, a: al[i], m: Mod(at[i], al[i])))
    }
    unsafe "W8 is written through its address, as a var is outside an atomic" {
        raw.Write(raw.PtrCast[L8K, u64](raw.Of(&W8)), R8.a + Trunc[u64](sys.args.Len()))
    }
    try Print(mut w, "values {a} {b} {c} {d} {e} {f} {g} {h} {i}\n", (a: P1.Len() + P2.Len() + P3.Len(), b: S8[1].a + S16[1].a,
        c: S64[1].a, d: T64[1].a, e: C8.a + C64.a, f: V8.a, g: V16.a, h: V64.a + Z64.a, i: W8.a))
}
