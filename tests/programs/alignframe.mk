// On x86_64 Windows, LLVM's prologue rounds the stack
// pointer of a frame aligned over a page down after its stack probe, by up
// to the alignment, onto pages a plain probe does not touch. Without a probe
// that covers them, the first store or call low in such a frame lands past the guard page of a
// stack that had not grown that far: an access violation with no report,
// in dev and release. Work16 and Work64 hold values aligned 16384 and
// 65536, filled and read by a run-time index so that release keeps them in
// the frame. Each trial calls one from a new depth, below every page an
// earlier trial touched. Prints how many trials read their values back.

align(16384) struct Big16 {
    a: [2048]u64
}

align(65536) struct Big64 {
    a: [8192]u64
}

// Work16 returns k times the sum of 0 to 2047.
Work16(k: int): u64 {
    var x := Big16(a: [0; 2048])
    for i in 0..2048 {
        x.a[i] = Trunc[u64](i * k)
    }
    var s: u64 = 0
    for i in 0..2048 {
        s += x.a[(i * 7 + k) % 2048]
    }
    return s
}

// Work64 returns k times the sum of 0 to 8191.
Work64(k: int): u64 {
    var x := Big64(a: [0; 8192])
    for i in 0..8192 {
        x.a[i] = Trunc[u64](i * k)
    }
    var s: u64 = 0
    for i in 0..8192 {
        s += x.a[(i * 7 + k) % 8192]
    }
    return s
}

// Down puts n frames of about a page each on the stack, then returns the
// trial's Work.
Down(n: int, k: int, big: bool): u64 {
    var pad: [500]u64 = [0; 500]
    pad[n % 500] = Trunc[u64](n)
    if n == 0 {
        if big {
            return Work64(k) + pad[0]
        }
        return Work16(k) + pad[0]
    }
    return Down(n - 1, k, big) + pad[n % 500] - Trunc[u64](n)
}

export Main(sys: &System): WriteError!void {
    var good := 0
    for k in 0..16 {
        if Down(1 + 9 * k, k, false) == Trunc[u64](k) * 2096128 {
            good += 1
        }
    }
    for k in 0..2 {
        if Down(150 + 33 * k, k, true) == Trunc[u64](k) * 33550336 {
            good += 1
        }
    }
    try sys.Print("{good} of 18\n", (good,))
}
