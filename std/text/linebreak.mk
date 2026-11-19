// Why: wrapping text to a width needs to know where a line may break, and
// for most scripts that is not "at a space".
//
// What: text.LineBreaks, the line-break opportunities of UAX 14 at the
// tables' Unicode version (linebreak_tables.mk), as byte offsets with their
// kind (mandatory or allowed), through the Breaks iterator. The file
// imports nothing.
//
// How: the rules are UAX 14's default ones, LB2 to LB31, as UCD
// 18.0.0's auxiliary/LineBreakTest.html lists them; the table already
// holds LB1's resolution (linebreak_tables.mk). Next scans forward one
// scalar at a time, keeping the left context the rules read in the
// iterator: the class of the last scalar (LB4 to LB9), the last two
// elements of the sequence after LB9 and LB10 (X (CM | ZWJ)* -> X, a lone
// CM or ZWJ -> AL), whether the left side ends in one of the X SP* forms of
// LB8, LB14, LB15a, LB16 and LB17, where it stands in NU (SY | IS)* (CL |
// CP)? (LB25), and the parity of its regional indicators (LB30a). The
// rules that look right (LB15b, LB15c, LB19a, LB25, LB28a) read at most two
// elements ahead, each skipping its marks once, so the walk is O(n).

/// An iterator over line-break opportunities, from text.LineBreaks: call
/// Next until none.
export view struct Breaks {
    text: str
    // pos is the offset of the next scalar to read; done is set after the
    // last opportunity.
    pos: int
    done: bool
    // raw is the class of the scalar before pos, p the element before pos
    // (class and flags), pp the element before p (LbSot at the start).
    raw: u8
    p: u8
    pp: u8
    // ctx holds the X SP* forms the left side ends in (Ctx*), num where it
    // stands in NU (SY | IS)* (CL | CP)? (Num*), ri whether the regional
    // indicators ending at p are odd in number.
    ctx: u8
    num: u8
    ri: bool
}

/// The places where a line of s may be wrapped, by Unicode's default
/// line-breaking rules (UAX 14), in increasing byte order. A line may end
/// at any opportunity and must end at a mandatory one: the one after a
/// line break character (a CR not followed by LF, LF, NEL, a form feed,
/// U+2028 and the rest of Line_Break classes BK, CR, LF and NL). Every
/// other opportunity is allowed. There is none at 0. The last is s.Len(),
/// mandatory only when s ends with a line break character. "" yields none.
/// O(1); allocates nothing.
export LineBreaks(s: str): Breaks {
    return Breaks(text: s, pos: 0, done: false, raw: LbAL, p: LbSot, pp: LbSot, ctx: 0, num: NumNone, ri: false)
}

/// The next opportunity: its byte offset, and whether the line must end
/// there; none after the last one. O(n) over the whole walk; one call is
/// O(length to the next opportunity). Allocates nothing.
export Breaks.Next(mut self): ?(at: int, mandatory: bool) {
    if self.done {
        return none
    }
    b := self.text.AsBytes()
    n := b.Len()
    if self.pos == 0 {
        if n == 0 {
            self.done = true
            return none
        }
        r := Decode(b, 0)
        qe := LinePropOf(r.c)
        qr := qe & 63
        self.Push(qr, LineElement(qe))
        self.pos = r.n
    }
    for self.pos < n {
        j := self.pos
        r := Decode(b, j)
        qe := LinePropOf(r.c)
        qr := qe & 63
        k := j + r.n
        if (qr == LbCM || qr == LbZWJ) && (LbNoAttach >> self.raw) & 1 == 0 {
            // LB9: a mark joins the element before it.
            self.raw = qr
            self.pos = k
            continue
        }
        q := LineElement(qe)
        v := self.Rule(b, k, qr, q)
        self.Push(qr, q)
        self.pos = k
        if v != LineJoin {
            return some((at: j, mandatory: v == LineMust))
        }
    }
    self.done = true
    return some((at: n, mandatory: (LbHard >> self.raw) & 1 == 1))
}

// The resolved Line_Break classes of a LineProp byte's low six bits, in
// the generator's order (std/std-textlines_ucd_test.go).
const LbAL: u8 = 0
const LbBK: u8 = 1
const LbCR: u8 = 2
const LbLF: u8 = 3
const LbNL: u8 = 4
const LbCM: u8 = 5
const LbZWJ: u8 = 6
const LbSP: u8 = 7
const LbZW: u8 = 8
const LbWJ: u8 = 9
const LbGL: u8 = 10
const LbBA: u8 = 11
const LbBB: u8 = 12
const LbB2: u8 = 13
const LbHY: u8 = 14
const LbHH: u8 = 15
const LbCB: u8 = 16
const LbCL: u8 = 17
const LbCP: u8 = 18
const LbEX: u8 = 19
const LbIN: u8 = 20
const LbNS: u8 = 21
const LbOP: u8 = 22
const LbQU: u8 = 23
const LbQuPi: u8 = 24
const LbQuPf: u8 = 25
const LbIS: u8 = 26
const LbNU: u8 = 27
const LbPO: u8 = 28
const LbPR: u8 = 29
const LbSY: u8 = 30
const LbHL: u8 = 31
const LbID: u8 = 32
const LbEB: u8 = 33
const LbEM: u8 = 34
const LbH2: u8 = 35
const LbH3: u8 = 36
const LbJL: u8 = 37
const LbJV: u8 = 38
const LbJT: u8 = 39
const LbRI: u8 = 40
const LbAK: u8 = 41
const LbAP: u8 = 42
const LbAS: u8 = 43
const LbVF: u8 = 44
const LbVI: u8 = 45
// U+25CC DOTTED CIRCLE, an AL that LB28a names.
const LbDc: u8 = 46
// The start of text, as a left context; the end of text, as a look-ahead.
const LbSot: u8 = 62
const LbEot: u8 = 63

// The LineProp flag bits.
const LbEastAsian: u8 = 0x40
const LbExtPictCn: u8 = 0x80

// Sets of classes, bit c for class c.
const LbHard: u64 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4)
// LB9: no mark joins these.
const LbNoAttach: u64 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 7) | (1 << 8)
// AL and HL, DottedCircle included.
const LbAlHl: u64 = (1 << 0) | (1 << 31) | (1 << 46)
const LbQuAll: u64 = (1 << 23) | (1 << 24) | (1 << 25)
const LbKorean: u64 = (1 << 35) | (1 << 36) | (1 << 37) | (1 << 38) | (1 << 39)
const LbAksara: u64 = (1 << 41) | (1 << 43) | (1 << 46)
// LB13: no break before these.
const LbCloser: u64 = (1 << 17) | (1 << 18) | (1 << 19) | (1 << 30)
// LB15a: what may stand before QU_Pi, sot included.
const LbBeforePi: u64 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 22) | (1 << 23) | (1 << 24) | (1 << 25) | (1 << 10) | (1 << 7) | (1 << 8) | (1 << 62)
// LB15b: what may follow QU_Pf, eot included.
const LbAfterPf: u64 = (1 << 7) | (1 << 10) | (1 << 9) | (1 << 17) | (1 << 23) | (1 << 24) | (1 << 25) | (1 << 18) | (1 << 19) | (1 << 26) | (1 << 30) | (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 8) | (1 << 63)
// LB20a: what may stand before the hyphen, sot included.
const LbBeforeHy: u64 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 4) | (1 << 7) | (1 << 8) | (1 << 16) | (1 << 10) | (1 << 62)
// LB21: no break before these.
const LbBeforeNo: u64 = (1 << 11) | (1 << 15) | (1 << 14) | (1 << 21)

// The X SP* forms of ctx.
const CtxZw: u8 = 1
const CtxOp: u8 = 2
const CtxPi: u8 = 4
const CtxClose: u8 = 8
const CtxB2: u8 = 16

// Where the left side stands in NU (SY | IS)* (CL | CP)?.
const NumNone: u8 = 0
const NumIn: u8 = 1
const NumClose: u8 = 2

// What Rule decides.
const LineJoin: u8 = 0
const LineMay: u8 = 1
const LineMust: u8 = 2

// LinePropOf is the LineProp byte of scalar c: a direct load for ASCII,
// else one three-stage lookup.
LinePropOf(c: u32): u8 {
    if c < 0x80 {
        ascii := &LineAscii
        return ascii[c]
    }
    i1 := &LinePropIndex1
    i2 := &LinePropIndex2
    data := &LinePropData
    b2 := u32(i1[c >> (LinePropShift2 + LinePropShift3)])
    b3 := u32(i2[(b2 << LinePropShift2) | ((c >> LinePropShift3) & ((u32(1) << LinePropShift2) - 1))])
    return data[(b3 << LinePropShift3) | (c & ((u32(1) << LinePropShift3) - 1))]
}

// LineElement is the element a scalar of LineProp qe starts: LB10 makes a
// lone CM or ZWJ an AL, with neither flag.
LineElement(qe: u8): u8 {
    q := qe & 63
    if q == LbCM || q == LbZWJ {
        return LbAL
    }
    return qe
}

// LbHas reports whether class c is in set.
LbHas(set: u64, c: u8): bool {
    return (set >> u64(c)) & 1 == 1
}

// LineAhead is the element that follows the scalar ending at k, whose
// class is raw: its marks skipped (LB9), its class and flags after LB10,
// and the offset after its first scalar; LbEot at the end of the text.
LineAhead(b: []u8, k: int, raw: u8): (e: u8, raw: u8, end: int) {
    n := b.Len()
    var j := k
    if !LbHas(LbNoAttach, raw) {
        for j < n {
            r := Decode(b, j)
            q := LinePropOf(r.c) & 63
            if q != LbCM && q != LbZWJ {
                break
            }
            j += r.n
        }
    }
    if j >= n {
        return (e: LbEot, raw: LbEot, end: n)
    }
    r := Decode(b, j)
    qe := LinePropOf(r.c)
    return (e: LineElement(qe), raw: qe & 63, end: j + r.n)
}

// Breaks.Push moves the left context past the element q, whose scalar
// has class qr.
Breaks.Push(mut self, qr: u8, q: u8) {
    c := q & 63
    p := self.p & 63
    old := self.ctx
    var ctx: u8 = 0
    if c == LbSP {
        ctx = old
    } else if c == LbZW {
        ctx = CtxZw
    } else if c == LbOP {
        ctx = CtxOp
    } else if c == LbQuPi && LbHas(LbBeforePi, p) {
        ctx = CtxPi
    } else if c == LbCL || c == LbCP {
        ctx = CtxClose
    } else if c == LbB2 {
        ctx = CtxB2
    }
    self.ctx = ctx
    self.num = if c == LbNU {
        NumIn
    } else if (c == LbSY || c == LbIS) && self.num == NumIn {
        NumIn
    } else if (c == LbCL || c == LbCP) && self.num == NumIn {
        NumClose
    } else {
        NumNone
    }
    self.ri = c == LbRI && !(p == LbRI && self.ri)
    self.pp = self.p
    self.p = q
    self.raw = qr
}

// Breaks.Rule decides the position before the element q, whose scalar
// has class qr and ends at k, by the first of LB4 to LB31 that applies.
Breaks.Rule(&self, b: []u8, k: int, qr: u8, qe: u8): u8 {
    raw := self.raw
    pe := self.p
    p := pe & 63
    q := qe & 63
    // LB4, LB5
    if raw == LbBK || raw == LbLF || raw == LbNL {
        return LineMust
    }
    if raw == LbCR {
        return if qr == LbLF { LineJoin } else { LineMust }
    }
    // LB6, LB7
    if LbHas(LbHard, q) || q == LbSP || q == LbZW {
        return LineJoin
    }
    // LB8, LB8a
    if self.ctx & CtxZw != 0 {
        return LineMay
    }
    if raw == LbZWJ {
        return LineJoin
    }
    // LB11, LB12, LB12a
    if q == LbWJ || p == LbWJ || p == LbGL {
        return LineJoin
    }
    if q == LbGL && p != LbSP && p != LbHY && p != LbHH {
        return LineJoin
    }
    // LB13, LB14, LB15a
    if LbHas(LbCloser, q) || self.ctx & (CtxOp | CtxPi) != 0 {
        return LineJoin
    }
    // LB15b
    if q == LbQuPf && LbHas(LbAfterPf, LineAhead(b, k, qr).e & 63) {
        return LineJoin
    }
    // LB15c, LB15d
    if q == LbIS {
        if p == LbSP && LineAhead(b, k, qr).e & 63 == LbNU {
            return LineMay
        }
        return LineJoin
    }
    // LB16, LB17
    if q == LbNS && self.ctx & CtxClose != 0 || q == LbB2 && self.ctx & CtxB2 != 0 {
        return LineJoin
    }
    // LB18
    if p == LbSP {
        return LineMay
    }
    // LB19
    if q == LbQU || q == LbQuPf || p == LbQU || p == LbQuPi {
        return LineJoin
    }
    // LB19a
    if LbHas(LbQuAll, q) {
        if pe & LbEastAsian == 0 {
            return LineJoin
        }
        next := LineAhead(b, k, qr).e
        if next == LbEot || next & LbEastAsian == 0 {
            return LineJoin
        }
    }
    if LbHas(LbQuAll, p) && (qe & LbEastAsian == 0 || self.pp == LbSot || self.pp & LbEastAsian == 0) {
        return LineJoin
    }
    // LB20
    if q == LbCB || p == LbCB {
        return LineMay
    }
    // LB20a
    pp := self.pp & 63
    hyphen := p == LbHY || p == LbHH
    if hyphen && LbHas(LbBeforeHy, pp) && LbHas(LbAlHl, q) {
        return LineJoin
    }
    // LB21, LB21a, LB21b, LB22
    if LbHas(LbBeforeNo, q) || p == LbBB || q == LbIN {
        return LineJoin
    }
    if hyphen && pp == LbHL && q != LbHL || p == LbSY && q == LbHL {
        return LineJoin
    }
    // LB23, LB23a, LB24
    if LbHas(LbAlHl, p) && (q == LbNU || q == LbPR || q == LbPO) || p == LbNU && LbHas(LbAlHl, q) {
        return LineJoin
    }
    if p == LbPR && (q == LbID || q == LbEB || q == LbEM) || (p == LbID || p == LbEB || p == LbEM) && q == LbPO {
        return LineJoin
    }
    prpo := p == LbPR || p == LbPO
    if prpo && LbHas(LbAlHl, q) {
        return LineJoin
    }
    // LB25
    if self.num == NumClose && (q == LbPO || q == LbPR) || self.num == NumIn && (q == LbPO || q == LbPR || q == LbNU) {
        return LineJoin
    }
    if prpo && q == LbNU || (p == LbHY || p == LbIS) && q == LbNU {
        return LineJoin
    }
    if prpo && q == LbOP {
        a := LineAhead(b, k, qr)
        if a.e & 63 == LbNU {
            return LineJoin
        }
        if a.e & 63 == LbIS && LineAhead(b, a.end, a.raw).e & 63 == LbNU {
            return LineJoin
        }
    }
    // LB26, LB27
    if p == LbJL && (q == LbJL || q == LbJV || q == LbH2 || q == LbH3) {
        return LineJoin
    }
    if (p == LbJV || p == LbH2) && (q == LbJV || q == LbJT) || (p == LbJT || p == LbH3) && q == LbJT {
        return LineJoin
    }
    if LbHas(LbKorean, p) && q == LbPO || p == LbPR && LbHas(LbKorean, q) {
        return LineJoin
    }
    // LB28, LB28a
    if LbHas(LbAlHl, p) && LbHas(LbAlHl, q) {
        return LineJoin
    }
    if p == LbAP && LbHas(LbAksara, q) || LbHas(LbAksara, p) && (q == LbVF || q == LbVI) {
        return LineJoin
    }
    if p == LbVI && LbHas(LbAksara, pp) && (q == LbAK || q == LbDc) {
        return LineJoin
    }
    if LbHas(LbAksara, p) && LbHas(LbAksara, q) && LineAhead(b, k, qr).e & 63 == LbVF {
        return LineJoin
    }
    // LB29, LB30
    if p == LbIS && LbHas(LbAlHl, q) {
        return LineJoin
    }
    if (LbHas(LbAlHl, p) || p == LbNU) && q == LbOP && qe & LbEastAsian == 0 {
        return LineJoin
    }
    if p == LbCP && pe & LbEastAsian == 0 && (LbHas(LbAlHl, q) || q == LbNU) {
        return LineJoin
    }
    // LB30a, LB30b
    if p == LbRI && q == LbRI && self.ri {
        return LineJoin
    }
    if q == LbEM && (p == LbEB || pe & LbExtPictCn != 0) {
        return LineJoin
    }
    return LineMay
}
