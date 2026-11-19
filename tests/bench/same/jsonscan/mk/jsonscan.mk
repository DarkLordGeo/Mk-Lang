// jsonscan, Mk: the reference algorithm of ../README.md. The file is read
// once with fs.ReadBytes into the arena, then scanned rounds times with b[i]
// (every index bounds checked). Indices and lengths are int with checked
// + (they never overflow); the token hash is u64 with +% and *% (overflow
// class "wraps"). The if chain is in the README's order; no match. Byte
// constants are written in hex, as std does.
// Usage: jsonscan <file> <rounds>. Prints the checksum.

import std.fs

const Space: u8 = 0x20
const Newline: u8 = 0x0A
const Return: u8 = 0x0D
const Tab: u8 = 0x09
const Quote: u8 = 0x22
const Backslash: u8 = 0x5C
const Minus: u8 = 0x2D
const Dot: u8 = 0x2E
const Zero: u8 = 0x30
const Nine: u8 = 0x39
const LowerT: u8 = 0x74
const LowerF: u8 = 0x66
const LowerN: u8 = 0x6E

Scan(b: []u8, h0: u64): u64 {
    var h := h0
    n := b.Len()
    var i := 0
    for i < n {
        c := b[i]
        if c == Space || c == Newline || c == Return || c == Tab {
            i += 1
        } else if c == Quote {
            i += 1
            var l := 0
            for i < n && b[i] != Quote {
                if b[i] == Backslash {
                    i += 1
                }
                i += 1
                l += 1
            }
            i += 1
            h = h *% 31 +% Trunc[u64](l)
        } else if c == Minus || (c >= Zero && c <= Nine) {
            var neg: u64 = 0
            if c == Minus {
                neg = 1
                i += 1
            }
            var v: u64 = 0
            for i < n && ((b[i] >= Zero && b[i] <= Nine) || b[i] == Dot) {
                if b[i] != Dot {
                    v = v *% 10 +% Cast[u64](b[i] - Zero)
                }
                i += 1
            }
            h = h *% 31 +% (v *% 2 +% neg)
        } else if c == LowerT {
            h = h *% 31 +% 1
            i += 4
        } else if c == LowerF {
            h = h *% 31 +% 2
            i += 5
        } else if c == LowerN {
            h = h *% 31 +% 3
            i += 4
        } else {
            h = h *% 31 +% Cast[u64](c)
            i += 1
        }
    }
    return h
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    b := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var total: u64 = 0
    for _ in 0..rounds {
        total = Scan(b, total)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
