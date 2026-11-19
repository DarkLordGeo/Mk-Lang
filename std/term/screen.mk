// Why: a full-screen program moves the cursor, clears, switches screens and
// prints untrusted text; each needs the right escape sequence, and text
// from outside must not smuggle sequences of its own.
//
// What: MoveTo, Clear, AltScreen, ShowCursor, BracketedPaste and Sanitize,
// with the helpers Suspect, Clamp and PutNum.
//
// How: the cursor and screen sequences of xterm's control sequences
// (ctlseqs: CUP, ED, DECSET 1049, 25 and 2004), which every terminal
// std.term targets honours, the Windows console included once
// virtual-terminal processing is on (Term.Raw turns it on). Each function
// writes one sequence to any Writer, so a program builds a frame in its own
// buffer and flushes it once.

import std.simd
import std.math

/// Moves the cursor to row and col, counted from 0 at the top left (the
/// sequence is CUP, which counts from 1). A negative row or col is 0, and
/// one past 65,534 is 65,534. Errors are w's. O(1); allocates nothing.
export MoveTo(w: mut Writer, row: int, col: int): WriteError!void {
    var b: [24]u8 = [0; 24]
    b[0] = 0x1B
    b[1] = b'['
    var n := PutNum(mut b[..], 2, Clamp(row) + 1)
    b[n] = b';'
    n = PutNum(mut b[..], n + 1, Clamp(col) + 1)
    b[n] = b'H'
    try w.Bytes(b[0..n + 1])
}

/// Clears the whole screen and moves the cursor to the top left (CUP, then
/// ED 2). Errors are w's. O(1); allocates nothing.
export Clear(w: mut Writer): WriteError!void {
    try w.Str("\x1b[H\x1b[2J")
}

/// Enters (on) or leaves the alternate screen, the full-screen buffer that
/// editors and pagers draw on, saving and restoring the cursor (xterm's
/// mode 1049). Written through Term.Writer, the switch is recorded, so
/// Term.Close, and the runtime on a panic or an exit, leave the alternate
/// screen; written to any other Writer, it is not. Errors are w's. O(1);
/// allocates nothing.
export AltScreen(w: mut Writer, on: bool): WriteError!void {
    try w.Str(if on { AltEnter } else { AltLeave })
}

/// Shows (on) or hides the cursor (mode 25). Errors are w's. O(1);
/// allocates nothing.
export ShowCursor(w: mut Writer, on: bool): WriteError!void {
    try w.Str(if on { "\x1b[?25h" } else { "\x1b[?25l" })
}

/// Turns bracketed paste on or off (mode 2004). On, Term.Read returns a
/// paste as Event.Paste events rather than as keys. On Windows the console
/// removes the paste markers while its input is not in virtual-terminal
/// mode, so the paste arrives as keys: call Term.Raw, which sets that mode,
/// before a paste can come. Errors are w's. O(1); allocates nothing.
export BracketedPaste(w: mut Writer, on: bool): WriteError!void {
    try w.Str(if on { "\x1b[?2004h" } else { "\x1b[?2004l" })
}

/// Writes s with every control character replaced by U+FFFD, so untrusted
/// text (a file name, a peer's message) cannot move the cursor, change a
/// mode, set the title or answer a query. Replaced are U+0000 to U+001F
/// except line feed and tab, DEL (U+007F), and U+0080 to U+009F, which
/// some terminals obey; every other scalar is written as it is.
///
/// Errors are w's. O(n): short safe runs and the replacements reach w in
/// blocks of up to 512 bytes, and a run of 510 bytes or more in one
/// w.Bytes call. It allocates nothing; the block is on the stack.
export Sanitize(w: mut Writer, s: str): WriteError!void {
    b := s.AsBytes()
    // Short safe runs and the replacements are staged in st and handed to
    // w a block at a time (one indirect call per block, not per run); a run
    // too long for the block goes to w whole, after what is staged.
    var st: [512]u8 = [0; 512]
    var n := 0
    var run := 0
    var i := 0
    for i < b.Len() {
        i = Suspect(b, i)
        if i >= b.Len() {
            break
        }
        c := b[i]
        var bad := 0
        if c < 0x20 && c != 0x0A && c != 0x09 || c == 0x7F {
            bad = 1
        } else if c == 0xC2 && i + 1 < b.Len() && b[i + 1] >= 0x80 && b[i + 1] <= 0x9F {
            bad = 2
        }
        if bad == 0 {
            i += 1
            continue
        }
        m := i - run
        if m + 3 > 512 - n {
            if n > 0 {
                try w.Bytes(st[0..n])
                n = 0
            }
        }
        if m + 3 > 512 {
            try w.Bytes(b[run..i])
        } else {
            (mut st[n..n + m]).CopyFrom(b[run..i])
            n += m
        }
        st[n] = 0xEF
        st[n + 1] = 0xBF
        st[n + 2] = 0xBD
        n += 3
        i += bad
        run = i
    }
    if n > 0 {
        try w.Bytes(st[0..n])
    }
    if b.Len() > run {
        try w.Bytes(b[run..])
    }
}

// Suspect is the index of the first byte at or after i that may need
// replacing (below 0x20 but LF and tab, 0x7F, or 0xC2, a C1 control's
// lead), or b.Len(): sixteen bytes a step by five vector compares, the
// classification step of simdutf's validators, the index taken from the
// mask's trailing zeros; then one byte at a time for the tail.
Suspect(b: []u8, i: int): int {
    space := simd.Vec[u8, 16].Splat(0x20)
    del := simd.Vec[u8, 16].Splat(0x7F)
    c1 := simd.Vec[u8, 16].Splat(0xC2)
    lf := simd.Vec[u8, 16].Splat(0x0A)
    tab := simd.Vec[u8, 16].Splat(0x09)
    var k := i
    for k + 16 <= b.Len() {
        v := simd.Vec[u8, 16].FromSlice(b, k)
        keep := (v == lf).Bits() | (v == tab).Bits()
        m := ((v < space).Bits() & ~keep) | (v == del).Bits() | (v == c1).Bits()
        if m != 0 {
            return k + math.TrailingZeros64(u64(m))
        }
        k += 16
    }
    for k < b.Len() {
        c := b[k]
        if c < 0x20 && c != 0x0A && c != 0x09 || c == 0x7F || c == 0xC2 {
            return k
        }
        k += 1
    }
    return k
}

// The alternate-screen sequences (DECSET and DECRST 1049); Term.Writer
// watches its output for them.
const AltEnter: str = "\x1b[?1049h"
const AltLeave: str = "\x1b[?1049l"

// The largest coordinate MoveTo writes: CUP's parameter is at most 65,535.
const CoordMax: int = 65_534

// Clamp is v within 0 to CoordMax.
Clamp(v: int): int {
    if v < 0 {
        return 0
    }
    if v > CoordMax {
        return CoordMax
    }
    return v
}

// PutNum writes v (0 to 99,999) in decimal at b[at..] and returns the
// index after it; b has room for five digits past at.
PutNum(b: mut []u8, at: int, v: int): int {
    var d: [5]u8 = [0; 5]
    var n := 0
    var x := v
    for {
        d[n] = Trunc[u8](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for k in 0..n {
        b[at + k] = d[n - 1 - k]
    }
    return at + n
}
