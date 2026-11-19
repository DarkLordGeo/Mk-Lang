// The sanitize row (README.md): n generated items of terminal text,
// sanitized rounds times by the same scalar scan the C, Rust, Zig and Go
// columns run, into one buffer allocated once (TERM-13, TERM-14: the
// same-algorithm table compares the same loop and the same data
// structures; std's term.Sanitize, which scans 16 bytes a step and writes
// through a Writer, is not this table's).

const G: u64 = 0x9e37_79b9_7f4a_7c15

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58_476d_1ce4_e5b9
    z = (z ^ (z >> 27)) *% 0x94d0_49bb_1331_11eb
    return z ^ (z >> 31)
}

// Gen appends n items to b and returns the length.
Gen(b: mut []u8, n: int): int {
    var st: u64 = 1
    var o := 0
    for _ in 0..n {
        st = st +% G
        z := Mix(st)
        k := z % 64
        v := Trunc[u8]((z >> 8) % 95)
        w := Trunc[u8]((z >> 8) % 32)
        if k < 48 {
            b[o] = 0x20 + v
            o += 1
        } else if k < 52 {
            b[o] = 0x0A
            o += 1
        } else if k < 56 {
            b[o] = 0xC3
            b[o + 1] = 0xA9
            o += 2
        } else if k < 58 {
            b[o] = 0x1B
            o += 1
        } else if k < 60 {
            b[o] = w
            o += 1
        } else if k < 62 {
            b[o] = 0xC2
            b[o + 1] = 0x80 + w
            o += 2
        } else {
            b[o] = 0xE2
            b[o + 1] = 0x82
            b[o + 2] = 0xAC
            o += 3
        }
    }
    return o
}

// Sanitize writes s to out with every C0 control but LF and tab, DEL and
// every C1 control (C2 80..9F) replaced by U+FFFD, each safe run copied
// whole, and returns the length: sanitize.c's loop.
Sanitize(out: mut []u8, s: []u8): int {
    n := s.Len()
    var o := 0
    var run := 0
    var i := 0
    for i < n {
        c := s[i]
        var bad := 0
        if c < 0x20 && c != 0x0A && c != 0x09 || c == 0x7F {
            bad = 1
        } else if c == 0xC2 && i + 1 < n && s[i + 1] >= 0x80 && s[i + 1] <= 0x9F {
            bad = 2
        }
        if bad == 0 {
            i += 1
            continue
        }
        (mut out[o..o + i - run]).CopyFrom(s[run..i])
        o += i - run
        out[o] = 0xEF
        out[o + 1] = 0xBF
        out[o + 2] = 0xBD
        o += 3
        i += bad
        run = i
    }
    (mut out[o..o + n - run]).CopyFrom(s[run..n])
    return o + n - run
}

export Main(sys: &System): OutOfMemory|ParseError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var b := try sys.arena.Slice[u8](3 * n)
    len := Gen(mut b[..], n)
    var out := try sys.arena.Slice[u8](9 * n)
    var total: u64 = 0
    for r in 0..rounds {
        m := Sanitize(mut out[..], b[0..len])
        total = total +% Cast[u64](m)
        total = total +% u64(out[(r * 7919) % m])
    }
    sys.Print("{total}\n", (total)) else {}
}
