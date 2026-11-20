// Why: Read's buffer handling (Windows records, UTF-16 joins, pastes, the
// Writer's mode scan) can be tested without any terminal.
// What: blocks over Bare Terms for RecordsFor, the paste search, the
// secret line, Close's wipe, AppendLossy and WatchCut.
// How: builds a Term from its fields, feeds it records or bytes and checks
// the fields after.

// Test blocks for Term.Read's buffer (read.mk) without a terminal: a Term
// built from its fields, fed Windows input records or bytes directly and
// decoded by Decode (Read's state machine but the wait and the read), then
// taken apart field by field (a Term with no handles has nothing to close).

// Bare is a Term with no handles, tail bytes of 'x' held, a pending high
// surrogate high (0 none) and pasting as given.
Bare(tail: int, high: u32, pasting: bool): Term {
    var t := Term(input: 0, out: 0, is_raw: false, saved_len: 0, saved: [0; 64], alt: false, alt_at: 0,
        in_buf: [0; 4096], head: 0, tail: tail, holding: false, held_at: 0, pasting: pasting, high: high, resized: false, empties: 0, cols: 0,
        rows: 0, reg: -1, mouse: false, mouse_at: 0, skip: 0, scan: 0, secret: [0; 1024], secret_len: 0,
        secret_raw: false, kitty: KittyZero())
    for i in 0..tail {
        t.in_buf[i] = b'x'
    }
    return t
}

// Drop takes a Bare Term apart.
Drop(t: Term) {
    Term(input: _, out: _, is_raw: _, saved_len: _, saved: _, alt: _, alt_at: _, in_buf: _, head: _, tail: _, holding: _, held_at: _, pasting: _,
        high: _, resized: _, empties: _, cols: _, rows: _, reg: _, mouse: _, mouse_at: _, skip: _, scan: _, secret: _, secret_len: _,
        secret_raw: _, kitty: _) := t
}

// KeyRecord is a KEY_EVENT key-down INPUT_RECORD carrying the UTF-16 unit u.
KeyRecord(u: u32): [20]u8 {
    var r: [20]u8 = [0; 20]
    r[0] = 1
    r[4] = 1
    r[14] = Trunc[u8](u & 0xFF)
    r[15] = Trunc[u8](u >> 8)
    return r
}

// Quiet is whether Decode finds no event in what t holds.
Quiet(t: mut Term, a: &Arena): bool {
    return !t.Decode(a, false, false).Must().IsSome()
}

// PasteLen is the length of the Paste Decode finds next, -1 for no event
// or another one.
PasteLen(t: mut Term, a: &Arena, secret: bool): int {
    e := t.Decode(a, secret, false).Must() else {
        return -1
    }
    return match e {
        .Paste(text) { text.Len() }
        _ { -1 }
    }
}

// Feed appends b to t's buffer, as a Linux read does (Fill's compaction
// first).
Feed(t: mut Term, b: []u8) {
    t.Compact()
    for c in b {
        t.in_buf[t.tail] = c
        t.tail += 1
    }
}

// Units are the record patterns RecordsFor must hold against: the euro
// sign after a lone high (6 bytes), highs alone, pairs, lows alone.
const Units: [6][4]u32 = [[0x20AC, 0x20AC, 0x20AC, 0x20AC], [0xD800, 0xD800, 0xD800, 0xD800], [0x20AC, 0xD800, 0x20AC, 0xD800],
    [0xD800, 0x20AC, 0xD800, 0x20AC], [0xDC00, 0xDC00, 0xDC00, 0xDC00], [0xD83D, 0xDE00, 0xD83D, 0xDE00]]

const Highs: [2]u32 = [0, 0xD800]

test "a read sized by RecordsFor never writes past the buffer, whatever the units" (sys: &System) {
    // A pending high surrogate before a unit that is no low surrogate adds
    // U+FFFD and the unit's own three bytes: six from one record.
    for free in 0..40 {
        for high in Highs {
            for u in Units {
                var t := Bare(InBytes - free, high, true)
                n := RecordsFor(free, t.high)
                if free >= Room {
                    Assert(n >= 1)
                }
                for k in 0..n {
                    r := KeyRecord(u[k % 4])
                    t.Record(r[..])
                }
                Assert(t.tail <= InBytes)
                Drop(t)
            }
        }
    }
}

test "a high surrogate left from before, then one record at 4 or 5 bytes free, is not read" (sys: &System) {
    Assert(RecordsFor(4, 0xD800) == 0 && RecordsFor(5, 0xD800) == 0)
    Assert(RecordsFor(6, 0xD800) == 1 && RecordsFor(8, 0xD800) == 1 && RecordsFor(9, 0xD800) == 2)
    var t := Bare(InBytes - 6, 0xD800, true)
    r := KeyRecord(0x20AC)
    t.Record(r[..])
    Assert(t.tail == InBytes && t.high == 0)
    Drop(t)
}

test "a key-down with no unit is NUL for Ctrl with Space or 2, and dropped for a bare Ctrl or Space and for AltGr with either" (sys: &System) {
    var t := Bare(0, 0, false)
    var r := KeyRecord(0)
    r[10] = 0x11
    r[16] = 0x08
    t.Record(r[..]) // Ctrl alone
    r[10] = 0x20
    r[16] = 0
    t.Record(r[..]) // Space with no unit and no Ctrl
    r[10] = 0x32
    r[16] = 0x09
    t.Record(r[..]) // AltGr+2 with no character
    r[10] = 0x20
    t.Record(r[..]) // AltGr+Space with no character
    Assert(t.tail == 0)
    r[16] = 0x08
    t.Record(r[..]) // Ctrl+Space
    r[10] = 0x32
    r[16] = 0x04
    t.Record(r[..]) // right Ctrl+2
    Assert(t.tail == 2 && t.in_buf[0] == 0 && t.in_buf[1] == 0)
    Drop(t)
}

test "a paste's end marker is found wherever the reads split it" (sys: &System) {
    a := sys.arena
    end := PasteEnd.AsBytes()
    for cut in 0..end.Len() {
        var t := Bare(0, 0, true)
        var body: [40]u8 = [b'p'; 40]
        Feed(mut t, body[..])
        Assert(Quiet(mut t, a) && t.pasting)
        Assert(t.scan == 40 - (end.Len() - 1))
        Feed(mut t, end[0..cut])
        Assert(Quiet(mut t, a) && t.pasting)
        Feed(mut t, end[cut..])
        Feed(mut t, "q".AsBytes())
        Assert(PasteLen(mut t, a, false) == 40 && !t.pasting)
        Drop(t)
    }
}

test "a paste fed a byte at a time keeps its search offset, and a full buffer is cut whole" (sys: &System) {
    a := sys.arena
    var t := Bare(0, 0, true)
    var joined := 0
    for i in 0..5000 {
        Feed(mut t, if i % 7 == 0 { "\x1b".AsBytes() } else { "z".AsBytes() })
        if e := t.Decode(a, false, false).Must() {
            match e {
                .Paste(text) { joined += text.Len() }
                _ { Assert(false) }
            }
        }
        Assert(t.scan <= t.tail - t.head && t.tail <= InBytes)
    }
    Feed(mut t, PasteEnd.AsBytes())
    if e := t.Decode(a, false, false).Must() {
        match e {
            .Paste(text) { joined += text.Len() }
            _ { Assert(false) }
        }
    }
    Assert(joined == 5000 && !t.pasting)
    Drop(t)
}

test "a paste in a secret goes into the secret line, not the arena, and the buffer keeps no copy" (sys: &System) {
    a := sys.arena
    var t := Bare(0, 0, false)
    Feed(mut t, "\x1b[200~pa\r\nssw\x1b[201~".AsBytes())
    Assert(PasteLen(mut t, a, true) == 0)
    Assert(str.From(t.secret[0..t.secret_len]).Must() == "passw")
    for i in 0..InBytes {
        Assert(t.in_buf[i] == 0)
    }
    // Keys typed after it stay held; what was decoded is zeroed.
    Feed(mut t, "ab".AsBytes())
    _ = t.Decode(a, true, false).Must()
    Assert(t.in_buf[0] == 0 && t.in_buf[1] == b'b')
    t.Compact()
    Assert(t.in_buf[0] == b'b' && t.in_buf[1] == 0)
    Drop(t)
}

test "a secret's paste past 1,024 bytes is ignored past the bound, in pieces of the buffer" (sys: &System) {
    a := sys.arena
    var t := Bare(0, 0, false)
    Feed(mut t, "\x1b[200~".AsBytes())
    _ = t.Decode(a, true, false).Must()
    var chunk: [512]u8 = [b'k'; 512]
    for _ in 0..40 {
        Feed(mut t, chunk[0..t.ChunkRoom()])
        _ = t.Decode(a, true, false).Must()
    }
    Feed(mut t, PasteEnd.AsBytes())
    _ = t.Decode(a, true, false).Must()
    Assert(t.secret_len == SecretMax && !t.pasting)
    Drop(t)
}

test "Close's first half wipes a kept secret and the input held" (sys: &System) {
    var t := Bare(10, 0, false)
    t.secret_len = Append(mut t.secret[..], 0, 's')
    t.secret_len = Append(mut t.secret[..], t.secret_len, 'k')
    t.Leave(sys.io)
    Assert(t.secret_len == 0)
    for i in 0..SecretMax {
        Assert(t.secret[i] == 0)
    }
    for i in 0..InBytes {
        Assert(t.in_buf[i] == 0)
    }
    Drop(t)
}

// ChunkRoom is how much of a 512-byte chunk fits in the buffer now.
Term.ChunkRoom(&self): int {
    free := InBytes - self.tail
    return if free < 512 { free } else { 512 }
}

test "AppendLossy leaves out line ends and makes each ill-formed subpart U+FFFD" (sys: &System) {
    var line: [9]u8 = [0; 9]
    var b: [7]u8 = [b'a', 0x0D, 0x0A, 0xFF, 0xE2, 0x82, b'b']
    n := AppendLossy(mut line[..], 0, b[..])
    Assert(str.From(line[0..n]).Must() == "a\u{FFFD}\u{FFFD}b")
    // Past the bound: what does not fit is left out.
    m := AppendLossy(mut line[..], n, "xyz".AsBytes())
    Assert(n == 8 && m == 9 && line[8] == b'x')
}

test "a failed send records what the bytes written did, and a set of either mode in the rest as on, never a reset" (sys: &System) {
    // Four bytes of AltEnter written, the rest (which sets both) not.
    var t := Bare(0, 0, false)
    t.WatchCut("\x1b[?1049h\x1b[?1000hx".AsBytes(), 4)
    Assert(t.alt && t.mouse)
    // AltEnter written; the reset in the rest is not taken.
    var u := Bare(0, 0, false)
    u.WatchCut("\x1b[?1049h\x1b[?1049l".AsBytes(), 8)
    Assert(u.alt && !u.mouse)
    // A reset written is taken.
    var v := Bare(0, 0, false)
    v.alt = true
    v.WatchCut("\x1b[?1049lzz".AsBytes(), 8)
    Assert(!v.alt)
    // Nothing sets: off, and the match length is the written bytes'.
    var x := Bare(0, 0, false)
    x.WatchCut("ab\x1b[?10".AsBytes(), 4)
    Assert(!x.alt && !x.mouse && x.alt_at == 2 && x.mouse_at == 2)
    // A count past the bytes is the whole send.
    var y := Bare(0, 0, false)
    y.WatchCut("\x1b[?1049h".AsBytes(), 99)
    Assert(y.alt && y.alt_at == 0)
    // The kitty flags: a push written counts, and so does a push
    // in the rest; a pop in the rest does not, and a pop written does.
    var k1 := Bare(0, 0, false)
    k1.WatchCut("\x1b[>1u\x1b[<u".AsBytes(), 5)
    Assert(k1.kitty.main == 1 && k1.kitty.alt == 0)
    var k2 := Bare(0, 0, false)
    k2.WatchCut("\x1b[>1u\x1b[>1u".AsBytes(), 5)
    Assert(k2.kitty.main == 2 && k2.kitty.alt == 0)
    var k3 := Bare(0, 0, false)
    k3.WatchCut("\x1b[>1u\x1b[>1u\x1b[<u".AsBytes(), 14)
    Assert(k3.kitty.main == 1)
    // A rest that enters the alternate screen and then pushes: the push
    // is the alternate screen's.
    var k4 := Bare(0, 0, false)
    k4.WatchCut("x\x1b[?1049h\x1b[>1u".AsBytes(), 1)
    Assert(k4.alt && k4.kitty.alt == 1 && k4.kitty.main == 0)
    Drop(k1)
    Drop(k2)
    Drop(k3)
    Drop(k4)
    Drop(t)
    Drop(u)
    Drop(v)
    Drop(x)
    Drop(y)
}
