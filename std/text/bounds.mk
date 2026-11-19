// Why: editors, search and text layout need to find words and sentences
// the way Unicode defines them, which no ASCII rule gets right.
//
// What: text.WordBounds and text.SentenceBounds, the word and sentence
// boundaries of UAX 29 at the tables' Unicode version (bounds_tables.mk),
// as byte offsets, through the Bounds iterator. The file imports nothing,
// though std.text is in layer 1.
//
// How: each call of Bounds.Next scans forward from the last boundary to the
// next one, once, keeping the left context the rules need in a few locals:
// the last two elements of the transformed sequence (WB4: X (Extend | Format
// | ZWJ)* -> X; SB5: X (Extend | Format)* -> X), the regional indicators
// ending the left side (WB15, WB16), and where the left side stands in SATerm
// Close* Sp* (SB8 to SB11). A boundary's state equals the start of text's for
// every rule, so the scan restarts there. The rules that look right (WB6,
// WB7b, WB12 one element ahead; SB8 to the first OLetter, Upper, Lower,
// ParaSep or SATerm) scan ahead once per Mid or SATerm sequence, and those
// scans never overlap, so every input costs O(n) for the whole walk: a scalar
// is decoded at most three times by WordBounds (a scan that reads it as the
// look-ahead of a failed WB6, WB7b or WB12, the scan that breaks before it,
// and the scan that starts at it) and at most twice by SentenceBounds. A
// Bounds holds the scanner as a fn value, so a program that names only one of
// the two links only that one's scanner and tables.

/// An iterator over boundaries, from text.WordBounds,
/// text.SentenceBounds or text.ParagraphBounds: call Next until none.
export view struct Bounds {
    text: str
    // pos is the last boundary reported, -1 before the first.
    pos: int
    // end is WordEnd or SentenceEnd: a fn value, not a flag, so that
    // Next references neither scanner and WordBounds alone does not link
    // the sentence tables.
    end: fn([]u8, int): int
}

/// The word boundaries of s, by Unicode's default word rules (UAX 29), as
/// byte offsets in increasing order: 0, each boundary inside s, then
/// s.Len(). "" yields none. The text between two consecutive offsets is a
/// word, a run of spaces, a punctuation mark or another segment. O(1);
/// allocates nothing.
export WordBounds(s: str): Bounds {
    return Bounds(text: s, pos: -1, end: WordEnd)
}

/// The sentence boundaries of s, by Unicode's default sentence rules
/// (UAX 29), as byte offsets in increasing order: 0, each boundary inside
/// s, then s.Len(). "" yields none. O(1); allocates nothing.
export SentenceBounds(s: str): Bounds {
    return Bounds(text: s, pos: -1, end: SentenceEnd)
}

/// The next boundary, a byte offset into the text; none after the last
/// one. O(n) over the whole walk for every input, each scalar decoded at
/// most three times (twice for SentenceBounds). One call is O(segment
/// length), except that SentenceBounds may read ahead after a full stop to
/// the next letter, paragraph separator or sentence terminator to decide
/// whether the sentence ends there. Allocates nothing.
export Bounds.Next(mut self): ?int {
    n := self.text.Len()
    i := self.pos
    if i < 0 {
        if n == 0 {
            return none
        }
        self.pos = 0
        return some(0)
    }
    if i >= n {
        return none
    }
    b := self.text.AsBytes()
    scan := self.end
    end := scan(b, i)
    self.pos = end
    return some(end)
}

// The Word_Break values of a WordProp byte's low five bits.
const WbOther: u8 = 0
const WbCR: u8 = 1
const WbLF: u8 = 2
const WbNewline: u8 = 3
const WbExtend: u8 = 4
const WbZWJ: u8 = 5
const WbRI: u8 = 6
const WbFormat: u8 = 7
const WbKatakana: u8 = 8
const WbHebrewLetter: u8 = 9
const WbALetter: u8 = 10
const WbSingleQuote: u8 = 11
const WbDoubleQuote: u8 = 12
const WbMidNumLet: u8 = 13
const WbMidLetter: u8 = 14
const WbMidNum: u8 = 15
const WbNumeric: u8 = 16
const WbExtendNumLet: u8 = 17
const WbWSegSpace: u8 = 18

const WordExtPict: u8 = 0x20

// Sets of Word_Break values, bit v for value v.
const WbNewlines: u32 = (1 << 1) | (1 << 2) | (1 << 3)
const WbIgnored: u32 = (1 << 4) | (1 << 5) | (1 << 7)
const WbAHLetter: u32 = (1 << 9) | (1 << 10)
const WbMidLetQ: u32 = (1 << 11) | (1 << 13) | (1 << 14)
const WbMidNumQ: u32 = (1 << 11) | (1 << 13) | (1 << 15)
// Letters and digits: WB5 and WB8 to WB10 join any two.
const WbAlnum: u32 = (1 << 9) | (1 << 10) | (1 << 16)

// Bit q of WordJoin[p] is set when WB5, WB7a, WB8 to WB10 or WB13 to
// WB13b join an element of class p to a following one of class q; the
// rules with more context are in WordJoins.
const WordJoin: [19]u32 = [
    0,                                                        // Other
    0,                                                        // CR
    0,                                                        // LF
    0,                                                        // Newline
    0,                                                        // Extend
    0,                                                        // ZWJ
    0,                                                        // Regional_Indicator
    0,                                                        // Format
    (1 << 8) | (1 << 17),                                     // Katakana: WB13, WB13a
    (1 << 9) | (1 << 10) | (1 << 11) | (1 << 16) | (1 << 17), // Hebrew_Letter: WB5, WB7a, WB9, WB13a
    (1 << 9) | (1 << 10) | (1 << 16) | (1 << 17),             // ALetter: WB5, WB9, WB13a
    0,                                                        // Single_Quote
    0,                                                        // Double_Quote
    0,                                                        // MidNumLet
    0,                                                        // MidLetter
    0,                                                        // MidNum
    (1 << 9) | (1 << 10) | (1 << 16) | (1 << 17),             // Numeric: WB8, WB10, WB13a
    (1 << 8) | (1 << 9) | (1 << 10) | (1 << 16) | (1 << 17),  // ExtendNumLet: WB13a, WB13b
    0,                                                        // WSegSpace
]

// WordPropOf is the WordProp byte of scalar c: a direct load for ASCII,
// else one three-stage lookup.
WordPropOf(c: u32): u8 {
    if c < 0x80 {
        ascii := &WordAscii
        return ascii[c]
    }
    i1 := &WordPropIndex1
    i2 := &WordPropIndex2
    data := &WordPropData
    b2 := u32(i1[c >> (WordPropShift2 + WordPropShift3)])
    b3 := u32(i2[(b2 << WordPropShift2) | ((c >> WordPropShift3) & ((u32(1) << WordPropShift2) - 1))])
    return data[(b3 << WordPropShift3) | (c & ((u32(1) << WordPropShift3) - 1))]
}

// WordEnd is the offset of the first word boundary after b[i], i < n.
WordEnd(b: []u8, i: int): int {
    n := b.Len()
    first := Decode(b, i)
    // raw is the class of the scalar before j; p and pp the last two
    // elements of the transformed sequence (pp Other at the start, which
    // no rule reads as a left context); ri the regional indicators
    // ending at p.
    var raw := WordPropOf(first.c) & 31
    var p := raw
    var pp := WbOther
    var ri := if raw == WbRI { 1 } else { 0 }
    var j := i + first.n
    ascii := &WordAscii
    for j < n {
        c := b[j]
        if c < 0x80 && (WbAlnum >> p) & 1 == 1 && (WbAlnum >> ascii[c]) & 1 == 1 {
            // An ASCII letter or digit after a letter or digit: WB5, WB8,
            // WB9 or WB10 joins it whatever raw is (raw is p, or an
            // Extend, Format or ZWJ absorbed into p).
            pp = p
            p = ascii[c]
            raw = p
            ri = 0
            j += 1
            continue
        }
        r := Decode(b, j)
        qe := WordPropOf(r.c)
        q := qe & 31
        if (WbNewlines >> raw) & 1 == 1 || (WbNewlines >> q) & 1 == 1 {
            // WB3 joins CR LF; WB3a and WB3b break around the rest.
            if raw != WbCR || q != WbLF {
                break
            }
        } else if !(raw == WbZWJ && qe & WordExtPict != 0) && !(raw == WbWSegSpace && q == WbWSegSpace) && (WbIgnored >> q) & 1 == 0 {
            // Past WB3c, WB3d and WB4: the rules over elements.
            if !WordJoins(b, j + r.n, p, pp, q, ri) {
                break
            }
        }
        if (WbIgnored >> q) & 1 == 0 {
            pp = p
            p = q
            ri = if q == WbRI { ri + 1 } else { 0 }
        }
        raw = q
        j += r.n
    }
    return j
}

// WordJoins reports whether WB5 to WB16 join element p (after pp) to the
// element q, whose scalar ends at k; ri is the regional indicators ending
// at p.
WordJoins(b: []u8, k: int, p: u8, pp: u8, q: u8, ri: int): bool {
    tab := &WordJoin
    if (tab[p] >> q) & 1 == 1 {
        return true
    }
    if (WbAHLetter >> p) & 1 == 1 && (WbMidLetQ >> q) & 1 == 1 {
        // WB6: no other rule joins a letter to a mid.
        return (WbAHLetter >> WordNext(b, k)) & 1 == 1
    }
    if p == WbHebrewLetter && q == WbDoubleQuote {
        return WordNext(b, k) == WbHebrewLetter // WB7b
    }
    if p == WbNumeric && (WbMidNumQ >> q) & 1 == 1 {
        return WordNext(b, k) == WbNumeric // WB12
    }
    if (WbAHLetter >> q) & 1 == 1 {
        // WB7, WB7c
        return (WbAHLetter >> pp) & 1 == 1 && (WbMidLetQ >> p) & 1 == 1 || pp == WbHebrewLetter && p == WbDoubleQuote && q == WbHebrewLetter
    }
    if q == WbNumeric {
        return pp == WbNumeric && (WbMidNumQ >> p) & 1 == 1 // WB11
    }
    return p == WbRI && q == WbRI && ri & 1 == 1 // WB15, WB16
}

// WordNext is the class of the first element at or after b[k] (WB4 skips
// Extend, Format and ZWJ), Other at the end of the text.
WordNext(b: []u8, k: int): u8 {
    n := b.Len()
    var j := k
    for j < n {
        r := Decode(b, j)
        q := WordPropOf(r.c) & 31
        if (WbIgnored >> q) & 1 == 0 {
            return q
        }
        j += r.n
    }
    return WbOther
}

// The Sentence_Break values of a SentProp byte.
const SbOther: u8 = 0
const SbCR: u8 = 1
const SbLF: u8 = 2
const SbSep: u8 = 3
const SbExtend: u8 = 4
const SbFormat: u8 = 5
const SbSp: u8 = 6
const SbLower: u8 = 7
const SbUpper: u8 = 8
const SbOLetter: u8 = 9
const SbNumeric: u8 = 10
const SbATerm: u8 = 11
const SbSTerm: u8 = 12
const SbClose: u8 = 13
const SbSContinue: u8 = 14

// Sets of Sentence_Break values, bit v for value v.
const SbParaSep: u32 = (1 << 1) | (1 << 2) | (1 << 3)
const SbIgnored: u32 = (1 << 4) | (1 << 5)
const SbSATerm: u32 = (1 << 11) | (1 << 12)
// SB8a, SB9 and SB10 join SATerm Close* Sp* to these.
const SbAfterTerm: u32 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 6) | (1 << 11) | (1 << 12) | (1 << 14)
// SB8's look-ahead stops at these.
const SbStop: u32 = (1 << 1) | (1 << 2) | (1 << 3) | (1 << 7) | (1 << 8) | (1 << 9) | (1 << 11) | (1 << 12)

// Where the left side stands in SATerm Close* Sp* (SB8 to SB11).
const SeqNone: u8 = 0
const SeqTerm: u8 = 1
const SeqSpace: u8 = 2

// SentPropOf is the SentProp byte of scalar c, as WordPropOf.
SentPropOf(c: u32): u8 {
    if c < 0x80 {
        ascii := &SentAscii
        return ascii[c]
    }
    i1 := &SentPropIndex1
    i2 := &SentPropIndex2
    data := &SentPropData
    b2 := u32(i1[c >> (SentPropShift2 + SentPropShift3)])
    b3 := u32(i2[(b2 << SentPropShift2) | ((c >> SentPropShift3) & ((u32(1) << SentPropShift2) - 1))])
    return data[(b3 << SentPropShift3) | (c & ((u32(1) << SentPropShift3) - 1))]
}

// SentenceEnd is the offset of the first sentence boundary after b[i],
// i < n.
SentenceEnd(b: []u8, i: int): int {
    n := b.Len()
    first := Decode(b, i)
    // raw, p and pp as in WordEnd; seq where p stands in SATerm Close*
    // Sp* (SeqTerm inside SATerm Close*, SeqSpace inside its Sp*), aterm
    // whether that SATerm is an ATerm.
    var raw := SentPropOf(first.c)
    var p := raw
    var pp := SbOther
    var seq := if (SbSATerm >> raw) & 1 == 1 { SeqTerm } else { SeqNone }
    var aterm := raw == SbATerm
    var j := i + first.n
    ascii := &SentAscii
    for j < n {
        c := b[j]
        if c < 0x80 && seq == SeqNone && (SbParaSep >> raw) & 1 == 0 && ((SbParaSep | SbSATerm) >> ascii[c]) & 1 == 0 {
            // Outside SATerm Close* Sp* and after no ParaSep, SB998 joins
            // an ASCII scalar that is neither a ParaSep nor an SATerm (SB6
            // and SB7 join too), and the sequence stays outside.
            pp = p
            p = ascii[c]
            raw = p
            j += 1
            continue
        }
        r := Decode(b, j)
        q := SentPropOf(r.c)
        if (SbParaSep >> raw) & 1 == 1 {
            // SB3 joins CR LF; SB4 breaks after the rest.
            if raw != SbCR || q != SbLF {
                break
            }
        } else if (SbIgnored >> q) & 1 == 0 {
            // Past SB5: the rules over elements.
            if !SentenceJoins(b, j + r.n, p, pp, q, seq, aterm) {
                break
            }
        }
        if (SbIgnored >> q) & 1 == 0 {
            seq = if (SbSATerm >> q) & 1 == 1 {
                SeqTerm
            } else if q == SbClose && seq == SeqTerm {
                SeqTerm
            } else if q == SbSp && seq != SeqNone {
                SeqSpace
            } else {
                SeqNone
            }
            aterm = if (SbSATerm >> q) & 1 == 1 { q == SbATerm } else { aterm }
            pp = p
            p = q
        }
        raw = q
        j += r.n
    }
    return j
}

// SentenceJoins reports whether SB6 to SB998 join element p (after pp)
// to the element q, whose scalar ends at k.
SentenceJoins(b: []u8, k: int, p: u8, pp: u8, q: u8, seq: u8, aterm: bool): bool {
    if p == SbATerm && (q == SbNumeric || q == SbUpper && (pp == SbUpper || pp == SbLower)) {
        return true // SB6, SB7
    }
    if seq == SeqNone {
        return true // SB998
    }
    if (SbAfterTerm >> q) & 1 == 1 || q == SbClose && seq == SeqTerm {
        return true // SB8a, SB9, SB10
    }
    // SB8 joins when a Lower follows before any OLetter, Upper, ParaSep
    // or SATerm; SB11 breaks otherwise. Reached once per SATerm Close*
    // Sp*, since the element after it ends the sequence. q is tested
    // first, so the look-ahead does not decode it again.
    if !aterm {
        return false
    }
    if (SbStop >> q) & 1 == 1 {
        return q == SbLower
    }
    return LowerFollows(b, k)
}

// LowerFollows reports whether the first scalar at or after b[k] whose
// class is in SbStop is a Lower.
LowerFollows(b: []u8, k: int): bool {
    n := b.Len()
    var j := k
    for j < n {
        r := Decode(b, j)
        q := SentPropOf(r.c)
        if (SbStop >> q) & 1 == 1 {
            return q == SbLower
        }
        j += r.n
    }
    return false
}
