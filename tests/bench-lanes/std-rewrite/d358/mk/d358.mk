// d358, Mk: std.core's str.From and str.Find against std.simd's ValidUtf8
// and IndexByte; this row measures
// how far they are from std.simd's. Modes: 0 str.From (core's ValidUtf8,
// 8-byte ASCII words), 1 simd.ValidUtf8 (Keiser-Lemire), 2 a one-byte
// str.Find (core's IndexByteIn, 32-byte SWAR), 3 simd.IndexByte. The buffer
// is validutf8's mixed 1- to 4-byte UTF-8 (a 64 KiB block repeated, splitmix64
// draws). Modes 0 and 1 plant a random byte within the last 64 bytes on half
// the reps; modes 2 and 3 search for one of 64 distinct ASCII bytes that only
// the last 64 bytes hold. Modes 2 and 3 both build the one-byte needle with
// str.From, so the two differ only in the search. Usage: d358 <size> <reps>
// <seed> <mode>. Prints the checksum, the same for modes 0 and 1 and for 2
// and 3.

import std.simd

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

const Enc: [64]u8 = [
    0x61, 0, 0, 0, 0x20, 0, 0, 0, 0x30, 0, 0, 0, 0x7E, 0, 0, 0,
    0xC2, 0x80, 0, 0, 0xDF, 0xBF, 0, 0, 0xC3, 0xA9, 0, 0, 0xD0, 0x96, 0, 0,
    0xE0, 0xA0, 0x80, 0, 0xED, 0x9F, 0xBF, 0, 0xEE, 0x80, 0x80, 0, 0xE4, 0xB8, 0x96, 0,
    0xF0, 0x90, 0x80, 0x80, 0xF4, 0x8F, 0xBF, 0xBF, 0xF0, 0x9F, 0x98, 0x80, 0xF3, 0xA0, 0x80, 0x80,
]

// TailByte is the k-th of 64 ASCII bytes no encoding of Enc holds:
// 0x21 to 0x60 without 0x30, then 0x7B.
TailByte(k: int): u8 {
    if k == 63 {
        return 0x7B
    }
    if k >= 15 {
        return Trunc[u8](0x22 + k)
    }
    return Trunc[u8](0x21 + k)
}

FromOk(b: []u8): bool {
    _ = str.From(b) else {
        return false
    }
    return true
}

export Main(sys: &System): OutOfMemory|ParseError|Utf8Error|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    mode := try sys.args[4].ParseInt()
    var blen := n
    if blen > 65536 {
        blen = 65536
    }
    blk := try sys.arena.Slice[u8](blen)
    var p := 0
    for {
        st = st +% 0x9e3779b97f4a7c15
        k := Cast[int](Mix(st) & 15)
        l := 1 + k / 4
        if p + l > blen {
            break
        }
        for j in 0..l {
            blk[p + j] = Enc[k * 4 + j]
        }
        p += l
    }
    (mut blk[p..blen]).Fill(0x61)
    buf := try sys.arena.Slice[u8](n)
    var i := 0
    for i + blen <= n {
        (mut buf[i..i + blen]).CopyFrom(blk[0..blen])
        i += blen
    }
    (mut buf[i..n]).Fill(0x61)
    var total: u64 = 0
    if mode < 2 {
        for _ in 0..reps {
            st = st +% 0x9e3779b97f4a7c15
            z := Mix(st)
            pos := n - 1 - Cast[int]((z >> 1) % 64)
            old := buf[pos]
            if z & 1 == 1 {
                buf[pos] = Trunc[u8](z >> 56)
            }
            var ok: u64 = 0
            if mode == 0 {
                if FromOk(buf[0..n]) {
                    ok = 1
                }
            } else if simd.ValidUtf8(buf[0..n]) {
                ok = 1
            }
            total = total *% 31 +% ok
            buf[pos] = old
        }
    } else {
        // The tail: no sequence may straddle n - 64.
        var j := n - 64
        for j > 0 && buf[j] & 0xC0 == 0x80 {
            j -= 1
        }
        if buf[n - 64] & 0xC0 == 0x80 {
            (mut buf[j..n - 64]).Fill(0x61)
        }
        for k in 0..64 {
            buf[n - 64 + k] = TailByte(k)
        }
        s := try str.From(buf[0..n])
        for _ in 0..reps {
            st = st +% 0x9e3779b97f4a7c15
            k := Cast[int](Mix(st) % 64)
            at := n - 64 + k
            needle := try str.From(buf[at..at + 1])
            var got := 0
            if mode == 2 {
                if f := s.Find(needle) {
                    got = f + 1
                }
            } else if f := simd.IndexByte(buf[0..n], needle.Byte(0)) {
                got = f + 1
            }
            total = total *% 31 +% Cast[u64](got)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
