// verdict: reject E0503
// note: the callee half in std: encoding.ParsePem decodes each block's DER into the arena and pushes the
// block into a List there, a store the callee drops, so the caller's der would hold no loan of sa. B keeps
// the loan of sa it steps through: E0503 at sa.Free. If accepted, the run reads freed arena memory (result 48).
// Mutant M-D791-keep accepts it
// The minimal form of P5-K4-p26x509parse. encoding.ParsePem(a: &Arena,
// text: str into a): Error|OutOfMemory!List[PemBlock] from a, text
// decodes each block's DER into a, so PemBlock.der is a view of a's
// own memory. With a static text, the caller's list holds only L(sa); a
// block read out of l.Done() holds B = held(sa), and no step of the call
// stored L(sa) into sa, so der holds nothing. sa is freed, its memory taken
// again with 'X', and der read: a dead read counts the DER's bytes as 'X'.
import std.encoding

Run(sys: &System): int {
    h := sys.heap
    var sa := Arena.New(h) else {
        return -1
    }
    l := encoding.ParsePem(&sa, "-----BEGIN DATA-----\nQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFB\n-----END DATA-----\n") else {
        sa.Free()
        return -2
    }
    bs := l.Done()
    if bs.Len() == 0 {
        sa.Free()
        return -3
    }
    der := bs[0].der
    sa.Free() // <- E0503
    return Refill(h, der)
}

// ---- helpers shared by the arena witnesses ----

// Clobber overwrites the stack below its caller's frame.
Clobber(v: i32): i32 {
    var buf: [256]i32 = [v; 256]
    var s: i32 = 0
    for i in 0..256 {
        buf[i] = buf[i] +% Trunc[i32](i)
        s = s +% buf[i]
    }
    return s
}

// CountX counts the bytes of b that are 'X' (88).
CountX(b: []u8): int {
    var n := 0
    for i in 0..b.Len() {
        if b[i] == 88 {
            n += 1
        }
    }
    return n
}

// Refill takes a second arena from h and fills 32 blocks of 64 bytes of it with 'X',
// so memory that a freed arena gave back to h is overwritten, and returns
// CountX(e), read while the second arena lives. A program that reads freed
// memory through e answers with the number of 'X' bytes it saw.
Refill(h: &Heap, e: []u8): int {
    var two := Arena.New(h) else {
        return -40
    }
    for _ in 0..32 {
        fill := two.Slice[u8](64) else {
            two.Free()
            return -50
        }
        for i in 0..fill.Len() {
            fill[i] = 88
        }
    }
    n := CountX(e)
    two.Free()
    return n
}

export Main(sys: &System): WriteError!void {
    r := Run(sys)
    try sys.Print("result {r}\n", (r))
}
