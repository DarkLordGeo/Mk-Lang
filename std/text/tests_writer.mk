// Why: text.Capped and text.Sink must keep their exact buffering and
// failure contract, since freestanding programs and panic hooks rely on it.
// What: test blocks of writer.mk, run by TestStdMkTests' std.text blocks.
// Helper names start with Tw.
// How: TwDevice stands for a device that records what reaches it and can
// be made to fail after a number of calls.

// TwDevice stands for a device: it keeps what reaches it in a fixed
// buffer, counts the write calls, and fails every call once `fail_at`
// calls have been made, as a device that stops answering would.
struct TwDevice {
    got: [64]u8
    n: int
    calls: int
    fail_at: int
}

TwNew(fail_at: int): TwDevice {
    return TwDevice(got: [0; 64], n: 0, calls: 0, fail_at: fail_at)
}

TwSend(d: mut TwDevice, b: []u8): WriteError!void {
    if d.calls == d.fail_at {
        fail WriteError
    }
    d.calls += 1
    for x in b {
        d.got[d.n] = x
        d.n += 1
    }
}

TwGot(d: &TwDevice): str {
    return str.From(d.got[..d.n]).Must()
}

const TwPort = Sink[TwDevice].New(TwSend)

test "a Capped keeps what fits, counts the rest and resets" (sys: &System) {
    _ = sys
    var b: [8]u8 = [0; 8]
    var c := Capped.New(mut b[..])
    var w := c.Writer()
    n := 42
    try Print(mut w, "n={n:>4}|", (n))
    Assert(str.From(c.Bytes()).Must() == "n=  42|")
    Assert(c.Dropped() == 0)
    var w2 := c.Writer()
    try Print(mut w2, "abcdef")
    Assert(str.From(c.Bytes()).Must() == "n=  42|a")
    Assert(c.Dropped() == 5)
    c.Reset()
    Assert(c.Bytes().Len() == 0 && c.Dropped() == 0)
}

test "a Sink with no buffer hands each write to its function" (sys: &System) {
    _ = sys
    var d := TwNew(-1)
    var empty: [0]u8 = []
    var w := (&TwPort).Writer(mut d, mut empty[..])
    try w.Str("ab")
    try w.Str("cd")
    try w.Flush()
    Assert(TwGot(&d) == "abcd")
    Assert(d.calls == 2)
}

test "a buffered Sink formats a line into one write at Flush" (sys: &System) {
    _ = sys
    var d := TwNew(-1)
    var buf: [48]u8 = [0; 48]
    var w := (&TwPort).Writer(mut d, mut buf[..])
    code: u32 = 6
    h: u64 = 0xBEEF
    x: i64 = -25
    try Print(mut w, "panic: {m} (code {code}) 0x{h:x} {x:+05}\n", (m: "must", code, h, x))
    try w.Flush()
    Assert(TwGot(&d) == "panic: must (code 6) 0xbeef -0025\n")
    Assert(d.calls == 1)
}

// TwLens records the length of each run its Sink's write receives.
struct TwLens {
    lens: [8]int
    calls: int
}

TwLensPut(r: mut TwLens, b: []u8): WriteError!void {
    r.lens[r.calls] = b.Len()
    r.calls += 1
}

const TwLensPort = Sink[TwLens].New(TwLensPut)

test "a buffered Sink tops its buffer up before a long run, which then goes on uncopied" (sys: &System) {
    _ = sys
    var r := TwLens(lens: [0; 8], calls: 0)
    var buf: [4]u8 = [0; 4]
    var w := (&TwLensPort).Writer(mut r, mut buf[..])
    try w.Str("ab")
    try w.Str("0123456789")
    try w.Str("cd")
    try w.Flush()
    // "ab01" fills the buffer, "23456789" goes on whole, "cd" waits for Flush
    Assert(r.calls == 3 && r.lens[0] == 4 && r.lens[1] == 8 && r.lens[2] == 2)
}

test "an unbuffered Sink makes one call per write that is not empty, none for an empty one" (sys: &System) {
    _ = sys
    var r := TwLens(lens: [0; 8], calls: 0)
    var empty: [0]u8 = []
    var w := (&TwLensPort).Writer(mut r, mut empty[..])
    try w.Str("")
    try w.Str("a")
    nothing: [0]u8 = []
    try w.Bytes(nothing[..])
    try w.Flush()
    Assert(r.calls == 1 && r.lens[0] == 1)
}

test "a buffered Sink passes a run longer than its buffer on whole when nothing is gathered" (sys: &System) {
    _ = sys
    var d := TwNew(-1)
    var buf: [4]u8 = [0; 4]
    var w := (&TwPort).Writer(mut d, mut buf[..])
    try w.Str("0123456789")
    try w.Flush()
    Assert(TwGot(&d) == "0123456789")
    Assert(d.calls == 1)
}

test "a Sink made at run time works as a const one does" (sys: &System) {
    _ = sys
    port := Sink[TwDevice].New(TwSend)
    var d := TwNew(-1)
    var buf: [16]u8 = [0; 16]
    var w := port.Writer(mut d, mut buf[..])
    try Print(mut w, "{a}-{b}", (a: 1, b: true))
    try w.Flush()
    Assert(TwGot(&d) == "1-true")
}

test "a Sink's failure is Print's WriteError" (sys: &System) {
    _ = sys
    var d := TwNew(0)
    var empty: [0]u8 = []
    var w := (&TwPort).Writer(mut d, mut empty[..])
    var failed := false
    Print(mut w, "x") else {
        failed = true
    }
    Assert(failed)
    Assert(d.n == 0)
}
