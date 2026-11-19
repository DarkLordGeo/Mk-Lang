// Why: text that mixes left-to-right and right-to-left scripts (Latin with
// Arabic or Hebrew) must be shown in an order that only the Unicode
// Bidirectional Algorithm decides.
//
// What: text.BidiLevels, the embedding levels of one paragraph by UAX 9's
// algorithm (rules P2 and P3, X1 to X10, W1 to W7, N0 to N2, I1 and I2),
// Levels.Line, a line's visual runs (L1 and L2), and text.Mirror, a
// scalar's Bidi_Mirroring_Glyph (L4), at the tables' Unicode version
// (bidi_tables.mk). Layer 1 of std.
//
// How: BidiLevels takes one pass per rule group over per-scalar arrays in a
// sub-arena: the classes, the explicit levels with a stack of 125 levels
// (BD2), BD9's matching isolate pairs, and each isolating run sequence (BD13)
// gathered into one index array in turn. A first-strong scan (P2, X5c) jumps
// over each nested isolate by its matching PDI, and BD16 keeps at most 63
// open brackets, so every input costs O(n): the bracket rule N0 reads a
// scalar once for each of the at most 63 pairs around it, and its look-back
// stops at the nearest strong type, which a resolved pair before it always
// is. The levels are kept per byte, so the text's scalars need no index.

/// The direction of a paragraph, for text.BidiLevels. Auto takes it from
/// the paragraph's first strong character outside an isolate, and is left
/// to right when there is none. Ltr and Rtl set it whatever the text holds.
export open enum Direction {
    /// From the paragraph's first strong character outside an isolate; left to
    /// right when there is none.
    Auto
    /// Left to right, whatever the text holds.
    Ltr
    /// Right to left, whatever the text holds.
    Rtl
}

/// The resolved embedding levels of one paragraph, from text.BidiLevels.
export view struct Levels {
    text: str
    // lv holds each scalar's level at each of its bytes.
    lv: []u8
    para: u8
}

/// A run of a line, from Levels.Line: the bytes start to end of the
/// paragraph's text, all at one embedding level. An odd level is right to
/// left. Reversing the clusters of such a run and drawing each mirrored
/// scalar as its Mirror glyph is the renderer's job.
export struct Run {
    /// The byte offset of the run's start in the paragraph's text.
    export start: int
    /// The byte offset where the run ends, exclusive.
    export end: int
    /// The embedding level; an odd level is right to left.
    export level: u8
}

// The Bidi_Class values of a BidiProp byte's low five bits.
const BcL: u8 = 0
const BcR: u8 = 1
const BcAL: u8 = 2
const BcEN: u8 = 3
const BcES: u8 = 4
const BcET: u8 = 5
const BcAN: u8 = 6
const BcCS: u8 = 7
const BcNSM: u8 = 8
const BcBN: u8 = 9
const BcB: u8 = 10
const BcS: u8 = 11
const BcWS: u8 = 12
const BcON: u8 = 13
const BcLRE: u8 = 14
const BcLRO: u8 = 15
const BcRLE: u8 = 16
const BcRLO: u8 = 17
const BcPDF: u8 = 18
const BcLRI: u8 = 19
const BcRLI: u8 = 20
const BcFSI: u8 = 21
const BcPDI: u8 = 22
// No strong type, for StrongOf and a first-strong scan.
const BcNone: u8 = 31

// The bracket bits of a BidiProp byte.
const BidiOpen: u8 = 0x20
const BidiClose: u8 = 0x40

// Sets of Bidi_Class values, bit v for value v.
const BcIsoInit: u32 = (1 << 19) | (1 << 20) | (1 << 21)
const BcIsoAll: u32 = (1 << 19) | (1 << 20) | (1 << 21) | (1 << 22)
// N1's neutral and isolate formatting types.
const BcNI: u32 = (1 << 10) | (1 << 11) | (1 << 12) | (1 << 13) | (1 << 19) | (1 << 20) | (1 << 21) | (1 << 22)
// L1's whitespace: WS, the isolate formatting characters, and what X9
// removes (BN and the embedding, override and pop characters).
const BcWsLike: u32 = (1 << 12) | (1 << 19) | (1 << 20) | (1 << 21) | (1 << 22) | (1 << 9) | (1 << 14) | (1 << 15) | (1 << 16) | (1 << 17) | (1 << 18)

// BD2: the deepest explicit level.
const MaxDepth: u8 = 125

// BD16: the most open brackets kept.
const MaxBrackets: int = 63

// BcHas reports whether class c is in set.
BcHas(set: u32, c: u8): bool {
    return (set >> c) & 1 == 1
}

// BidiPropOf is the BidiProp byte of scalar c: a direct load for ASCII,
// else one three-stage lookup.
BidiPropOf(c: u32): u8 {
    if c < 0x80 {
        ascii := &BidiAscii
        return ascii[c]
    }
    i1 := &BidiPropIndex1
    i2 := &BidiPropIndex2
    data := &BidiPropData
    b2 := u32(i1[c >> (BidiPropShift2 + BidiPropShift3)])
    b3 := u32(i2[(b2 << BidiPropShift2) | ((c >> BidiPropShift3) & ((u32(1) << BidiPropShift2) - 1))])
    return data[(b3 << BidiPropShift3) | (c & ((u32(1) << BidiPropShift3) - 1))]
}

// BidiFind is the index of c in the increasing table cps, or -1.
BidiFind(cps: []u32, c: u32): int {
    var lo := 0
    var hi := cps.Len()
    for lo < hi {
        mid := (lo + hi) / 2
        v := cps[mid]
        if v == c {
            return mid
        }
        if v < c {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return -1
}

// BracketKey is the match key of paired bracket c (bidi_tables.mk), 0
// for any other scalar.
BracketKey(c: u32): u32 {
    i := BidiFind(BidiBracketCp[..], c)
    if i < 0 {
        return 0
    }
    keys := &BidiBracketKey
    return keys[i]
}

/// The mirrored form of c (its Bidi_Mirroring_Glyph): the scalar a
/// renderer shows in its place at an odd level, such as ')' for '('. None
/// when c has none. O(log n) in the number of mirrored scalars; allocates
/// nothing.
export Mirror(c: char): ?char {
    i := BidiFind(BidiMirrorCp[..], u32(c))
    if i < 0 {
        return none
    }
    to := &BidiMirrorTo
    return some(Cast[char](to[i]))
}

// StrongOf is the strong direction type t counts as in N0 and N1: L, or
// R for R, EN and AN; BcNone for any other.
StrongOf(t: u8): u8 {
    if t == BcL {
        return BcL
    }
    if t == BcR || t == BcEN || t == BcAN {
        return BcR
    }
    return BcNone
}

// FirstStrong is P2's answer from scalar from on: BcL or BcR for the
// first L, or R or AL, outside an isolate, else BcNone. It stops at a
// paragraph separator, at an isolate initiator with no matching PDI (its
// isolate runs to the paragraph's end), and, when inside is set (X5c's
// scan of an FSI), at a PDI outside any nested isolate, which is the FSI's
// own; it jumps over a matched isolate to its PDI.
FirstStrong(orig: []u8, link: []int, from: int, inside: bool): u8 {
    m := orig.Len()
    var k := from
    for k < m {
        c := orig[k]
        if c == BcL {
            return BcL
        }
        if c == BcR || c == BcAL {
            return BcR
        }
        if BcHas(BcIsoInit, c) {
            if link[k] < 0 {
                return BcNone
            }
            k = link[k] + 1
            continue
        }
        if c == BcB || c == BcPDI && inside {
            return BcNone
        }
        k += 1
    }
    return BcNone
}

/// The paragraph boundaries of s, as byte offsets in increasing order: 0,
/// the offset after each paragraph separator inside s, then s.Len(). ""
/// yields none. A paragraph separator is a line feed, a carriage return
/// (CR LF counts as one), U+2029 or another scalar of Bidi_Class B. Each
/// paragraph keeps its separator. O(1); allocates nothing.
export ParagraphBounds(s: str): Bounds {
    return Bounds(text: s, pos: -1, end: ParagraphEnd)
}

// ParagraphEnd is the offset after the first paragraph separator at or
// after b[i], i < n, or n.
ParagraphEnd(b: []u8, i: int): int {
    n := b.Len()
    var j := i
    for j < n {
        r := Decode(b, j)
        j += r.n
        if BidiPropOf(r.c) & 31 == BcB {
            if r.c == 0x0D && j < n && b[j] == 0x0A {
                j += 1
            }
            return j
        }
    }
    return n
}

/// The embedding levels of s as one paragraph, by the Unicode
/// Bidirectional Algorithm (UAX 9), in a. dir is the paragraph's
/// direction, or Auto to take it from the text.
///
/// Split a text into paragraphs with ParagraphBounds first. A paragraph
/// separator inside s ends every embedding, override and isolate, and the
/// isolating run sequence, so each paragraph of s gets the levels it would
/// get alone with the same direction; Auto reads only the text before the
/// first separator.
///
/// Fails only with OutOfMemory. O(n) for every input; allocates n bytes of
/// levels in a, and about 40 bytes a scalar of scratch in a sub-arena of
/// a, freed before it returns.
export BidiLevels(a: &Arena, s: str, dir: Direction): OutOfMemory!Levels from a, s {
    b := s.AsBytes()
    n := b.Len()
    out := try a.Slice[u8](n)
    var m := 0
    for x in b {
        if x & 0xC0 != 0x80 {
            m += 1
        }
    }
    var w := try a.Sub()
    defer w.Free()
    off := try w.Slice[int](m + 1)
    orig := try w.Slice[u8](m)
    cls := try w.Slice[u8](m)
    lv := try w.Slice[u8](m)
    res := try w.Slice[u8](m)
    link := try w.Slice[int](m)
    sq := try w.Slice[int](m)
    pr := try w.Slice[int](m)
    var k := 0
    var pos := 0
    for pos < n {
        r := Decode(b, pos)
        off[k] = pos
        c := BidiPropOf(r.c) & 31
        orig[k] = c
        cls[k] = c
        link[k] = -1
        k += 1
        pos += r.n
    }
    off[m] = n
    // BD9: link joins each isolate initiator and its matching PDI; sq is
    // the stack of open initiators.
    var top := 0
    for i in 0..m {
        c := orig[i]
        if BcHas(BcIsoInit, c) {
            sq[top] = i
            top += 1
        } else if c == BcPDI {
            if top > 0 {
                top -= 1
                o := sq[top]
                link[o] = i
                link[i] = o
            }
        } else if c == BcB {
            top = 0
        }
    }
    base: u8 = if dir == Direction.Rtl {
        1
    } else if dir == Direction.Ltr {
        0
    } else if FirstStrong(orig[..], link[..], 0, false) == BcR {
        1
    } else {
        0
    }
    Explicit(orig[..], link[..], cls, lv, base)
    res.CopyFrom(lv[..])
    // X10: each isolating run sequence, from the level run that starts
    // it; a run that starts with a matched PDI continues the sequence of
    // its initiator. A paragraph separator ends its sequence, so the text
    // on each side of it resolves as its own paragraph would.
    var last := -1
    for i in 0..m {
        if cls[i] == BcBN {
            continue
        }
        if last >= 0 && lv[last] == lv[i] && orig[last] != BcB || orig[i] == BcPDI && link[i] >= 0 {
            last = i
            continue
        }
        level := lv[i]
        before := if last < 0 { base } else { lv[last] }
        var count := 0
        var j := i
        var end := i
        var next := m
        for {
            var x := j
            for x < m {
                if cls[x] != BcBN {
                    if lv[x] != level {
                        break
                    }
                    sq[count] = x
                    count += 1
                    end = x
                    if orig[x] == BcB {
                        x += 1
                        break
                    }
                }
                x += 1
            }
            next = x
            if BcHas(BcIsoInit, orig[end]) && link[end] >= 0 {
                j = link[end]
                continue
            }
            break
        }
        after := if BcHas(BcIsoInit, orig[end]) || orig[end] == BcB || next >= m { base } else { lv[next] }
        sos_level := if before > level { before } else { level }
        eos_level := if after > level { after } else { level }
        sos := if sos_level & 1 == 1 { BcR } else { BcL }
        eos := if eos_level & 1 == 1 { BcR } else { BcL }
        seq := sq[0..count]
        Weak(seq, cls, sos)
        Brackets(seq, b, off[..], orig[..], cls, mut pr[0..count], level, sos)
        Neutral(seq, cls, level, sos, eos)
        for x in seq {
            t := cls[x]
            if level & 1 == 0 {
                if t == BcR {
                    res[x] = level + 1
                } else if t == BcAN || t == BcEN {
                    res[x] = level + 2
                }
            } else if t == BcL || t == BcEN || t == BcAN {
                res[x] = level + 1
            }
        }
        last = i
    }
    // What X9 removed takes the level before it, so a run of the line
    // does not split there.
    for i in 0..m {
        if cls[i] == BcBN {
            res[i] = if i == 0 { base } else { res[i - 1] }
        }
        for p in off[i]..off[i + 1] {
            out[p] = res[i]
        }
    }
    return Levels(text: s, lv: out[..], para: base)
}

// Explicit applies X1 to X9: each scalar's explicit level in lv, the
// override's L or R in cls, and BN in cls for every scalar X9 removes.
Explicit(orig: []u8, link: []int, cls: mut []u8, lv: mut []u8, base: u8) {
    var st_level: [128]u8 = [0; 128]
    var st_over: [128]u8 = [0; 128]
    var st_iso: [128]bool = [false; 128]
    st_level[0] = base
    st_over[0] = BcNone
    var depth := 1
    var over_iso := 0
    var over_emb := 0
    var valid_iso := 0
    for k in 0..orig.Len() {
        c := orig[k]
        cur := st_level[depth - 1]
        over := st_over[depth - 1]
        if c == BcRLE || c == BcLRE || c == BcRLO || c == BcLRO {
            // X2 to X5
            lv[k] = cur
            cls[k] = BcBN
            level := if c == BcRLE || c == BcRLO { (cur + 1) | 1 } else { (cur + 2) & 0xFE }
            if level <= MaxDepth && over_iso == 0 && over_emb == 0 {
                st_level[depth] = level
                st_over[depth] = if c == BcRLO { BcR } else if c == BcLRO { BcL } else { BcNone }
                st_iso[depth] = false
                depth += 1
            } else if over_iso == 0 {
                over_emb += 1
            }
        } else if BcHas(BcIsoInit, c) {
            // X5a to X5c
            lv[k] = cur
            if over != BcNone {
                cls[k] = over
            }
            rtl := c == BcRLI || c == BcFSI && FirstStrong(orig, link, k + 1, true) == BcR
            level := if rtl { (cur + 1) | 1 } else { (cur + 2) & 0xFE }
            if level <= MaxDepth && over_iso == 0 && over_emb == 0 {
                valid_iso += 1
                st_level[depth] = level
                st_over[depth] = BcNone
                st_iso[depth] = true
                depth += 1
            } else {
                over_iso += 1
            }
        } else if c == BcPDI {
            // X6a
            if over_iso > 0 {
                over_iso -= 1
            } else if valid_iso > 0 {
                over_emb = 0
                for !st_iso[depth - 1] {
                    depth -= 1
                }
                depth -= 1
                valid_iso -= 1
            }
            lv[k] = st_level[depth - 1]
            if st_over[depth - 1] != BcNone {
                cls[k] = st_over[depth - 1]
            }
        } else if c == BcPDF {
            // X7
            lv[k] = cur
            cls[k] = BcBN
            if over_iso > 0 {
                // in an overflowed isolate: nothing
            } else if over_emb > 0 {
                over_emb -= 1
            } else if !st_iso[depth - 1] && depth >= 2 {
                depth -= 1
            }
        } else if c == BcB {
            // X8
            lv[k] = base
            depth = 1
            over_iso = 0
            over_emb = 0
            valid_iso = 0
        } else if c == BcBN {
            lv[k] = cur
        } else {
            // X6
            lv[k] = cur
            if over != BcNone {
                cls[k] = over
            }
        }
    }
}

// Weak applies W1 to W7 to the isolating run sequence seq.
Weak(seq: []int, cls: mut []u8, sos: u8) {
    count := seq.Len()
    // W1
    var prev := sos
    for x in seq {
        var t := cls[x]
        if t == BcNSM {
            t = if BcHas(BcIsoAll, prev) { BcON } else { prev }
            cls[x] = t
        }
        prev = t
    }
    // W2, W3
    var strong := sos
    for x in seq {
        t := cls[x]
        if t == BcEN {
            if strong == BcAL {
                cls[x] = BcAN
            }
        } else if t == BcL || t == BcR {
            strong = t
        } else if t == BcAL {
            strong = BcAL
            cls[x] = BcR
        }
    }
    // W4
    for i in 1..count - 1 {
        t := cls[seq[i]]
        if t == BcES || t == BcCS {
            before := cls[seq[i - 1]]
            after := cls[seq[i + 1]]
            if before == BcEN && after == BcEN {
                cls[seq[i]] = BcEN
            } else if t == BcCS && before == BcAN && after == BcAN {
                cls[seq[i]] = BcAN
            }
        }
    }
    // W5
    var i := 0
    for i < count {
        if cls[seq[i]] != BcET {
            i += 1
            continue
        }
        s := i
        for i < count && cls[seq[i]] == BcET {
            i += 1
        }
        if s > 0 && cls[seq[s - 1]] == BcEN || i < count && cls[seq[i]] == BcEN {
            for j in s..i {
                cls[seq[j]] = BcEN
            }
        }
    }
    // W6, W7
    strong = sos
    for x in seq {
        t := cls[x]
        if t == BcES || t == BcET || t == BcCS {
            cls[x] = BcON
        } else if t == BcL || t == BcR {
            strong = t
        } else if t == BcEN && strong == BcL {
            cls[x] = BcL
        }
    }
}

// Brackets applies N0 to seq: BD16's pairs, each opening position's
// partner in pr (-1 for none), then each pair in order of its opening
// bracket.
Brackets(seq: []int, b: []u8, off: []int, orig: []u8, cls: mut []u8, pr: mut []int, level: u8, sos: u8) {
    count := seq.Len()
    for i in 0..count {
        pr[i] = -1
    }
    var keys: [63]u32 = [0; 63]
    var at: [63]int = [0; 63]
    var depth := 0
    for i in 0..count {
        x := seq[i]
        if cls[x] != BcON {
            continue
        }
        c := Decode(b, off[x]).c
        prop := BidiPropOf(c)
        if prop & BidiOpen != 0 {
            if depth == MaxBrackets {
                break
            }
            keys[depth] = BracketKey(c)
            at[depth] = i
            depth += 1
        } else if prop & BidiClose != 0 {
            key := BracketKey(c)
            var d := depth - 1
            for d >= 0 && keys[d] != key {
                d -= 1
            }
            if d >= 0 {
                pr[at[d]] = i
                depth = d
            }
        }
    }
    e := if level & 1 == 0 { BcL } else { BcR }
    for i in 0..count {
        cl := pr[i]
        if cl < 0 {
            continue
        }
        var found := BcNone
        for j in i + 1..cl {
            t := StrongOf(cls[seq[j]])
            if t == e {
                found = e
                break
            }
            if t != BcNone {
                found = t
            }
        }
        if found == BcNone {
            continue
        }
        var dir := e
        if found != e {
            // N0 c: the opposite direction inside; the context before the
            // pair decides, sos when there is none.
            dir = sos
            var j := i - 1
            for j >= 0 {
                t := StrongOf(cls[seq[j]])
                if t != BcNone {
                    dir = t
                    break
                }
                j -= 1
            }
        }
        SetBracket(seq, orig, cls, i, dir)
        SetBracket(seq, orig, cls, cl, dir)
    }
}

// SetBracket gives the bracket at seq[side] the direction dir, with the
// scalars after it that were NSM before W1 (N0's last paragraph).
SetBracket(seq: []int, orig: []u8, cls: mut []u8, side: int, dir: u8) {
    cls[seq[side]] = dir
    for j in side + 1..seq.Len() {
        if orig[seq[j]] != BcNSM {
            break
        }
        cls[seq[j]] = dir
    }
}

// Neutral applies N1 and N2 to seq.
Neutral(seq: []int, cls: mut []u8, level: u8, sos: u8, eos: u8) {
    count := seq.Len()
    e := if level & 1 == 0 { BcL } else { BcR }
    var i := 0
    for i < count {
        if !BcHas(BcNI, cls[seq[i]]) {
            i += 1
            continue
        }
        s := i
        for i < count && BcHas(BcNI, cls[seq[i]]) {
            i += 1
        }
        lead := if s == 0 { sos } else { StrongOf(cls[seq[s - 1]]) }
        trail := if i == count { eos } else { StrongOf(cls[seq[i]]) }
        d := if lead == trail { lead } else { e }
        for j in s..i {
            cls[seq[j]] = d
        }
    }
}

/// The paragraph embedding level: 0 left to right, 1 right to left.
export Levels.Base(&self): u8 {
    return self.para
}

/// The resolved level of the scalar at byte offset i, which may be any
/// byte of the scalar. This is the level before line rules apply: Line
/// resets trailing whitespace and separators to the paragraph level. A
/// character the algorithm sets aside (an embedding, override or pop
/// control, or one of Bidi_Class BN) has the level of the scalar before
/// it, or the paragraph level at the start.
/// Panics unless 0 <= i < the text's length. O(1); allocates nothing.
export Levels.At(&self, i: int): u8 {
    return self.lv[i]
}

/// The runs of the line made of the paragraph's bytes start to end, in
/// display order from left to right, in a. Each run is a longest span of
/// one level, and the runs cover the line.
///
/// Segment and paragraph separators, and whitespace and isolate controls
/// before one or at the end of the line, take the paragraph level. Then
/// runs are reordered: for each odd level from the highest down, every
/// sequence of runs at or above it is reversed.
///
/// Panics unless start and end are scalar boundaries with start <= end.
/// Fails only with OutOfMemory. O(line length x levels); allocates the
/// runs in a, and the line's bytes of scratch in a sub-arena of a, freed
/// before it returns.
export Levels.Line(&self, a: &Arena, start: int, end: int): OutOfMemory![]Run from a {
    _ = self.text[start..end]
    b := self.text.AsBytes()
    base := self.para
    var w := try a.Sub()
    defer w.Free()
    len := end - start
    lvl := try w.Slice[u8](len)
    // L1: ws is the start of the run of whitespace before p, -1 for none.
    var ws := -1
    var p := start
    for p < end {
        r := Decode(b, p)
        c := BidiPropOf(r.c) & 31
        i := p - start
        var v := self.lv[p]
        if c == BcS || c == BcB {
            v = base
            if ws >= 0 {
                for q in ws..i {
                    lvl[q] = base
                }
            }
            ws = -1
        } else if BcHas(BcWsLike, c) {
            if ws < 0 {
                ws = i
            }
        } else {
            ws = -1
        }
        for q in i..i + r.n {
            lvl[q] = v
        }
        p += r.n
    }
    if ws >= 0 {
        for q in ws..len {
            lvl[q] = base
        }
    }
    var count := 0
    for q in 0..len {
        if q == 0 || lvl[q] != lvl[q - 1] {
            count += 1
        }
    }
    runs := try a.Slice[Run](count)
    var hi: u8 = 0
    var lo: u8 = 255
    var nr := 0
    for q in 0..len {
        if q > 0 && lvl[q] == lvl[q - 1] {
            continue
        }
        if nr > 0 {
            runs[nr - 1].end = start + q
        }
        v := lvl[q]
        runs[nr] = Run(start: start + q, end: end, level: v)
        nr += 1
        if v > hi {
            hi = v
        }
        if v & 1 == 1 && v < lo {
            lo = v
        }
    }
    // L2
    var level := hi
    for lo <= hi && level >= lo {
        var i := 0
        for i < count {
            if runs[i].level < level {
                i += 1
                continue
            }
            s := i
            for i < count && runs[i].level >= level {
                i += 1
            }
            var x := s
            var y := i - 1
            for x < y {
                t := runs[x]
                runs[x] = runs[y]
                runs[y] = t
                x += 1
                y -= 1
            }
        }
        level -= 1
    }
    return runs[..]
}
