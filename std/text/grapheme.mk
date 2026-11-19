// Why: what a user sees as one character (an accented letter, a flag, a
// family emoji) is often several scalars, and cursor movement, truncation
// and terminal layout must treat it as one.
//
// What: text.Graphemes, the extended grapheme clusters of UAX 29 at the
// tables' Unicode version, through the Clusters iterator, and text.Width,
// terminal columns per cluster. The file imports only std.raw and std.simd,
// though std.text is in layer 1.
//
// How: the rules are UCD 18.0.0's GraphemeBreakTest.html list: GB3 to GB9b
// are a pair table over Grapheme_Cluster_Break (NoBreak), and three flags
// carry the left context of the rest: RI parity (GB12, GB13), ExtPict Extend*
// ZWJ (GB11), and ConjunctLinker ConjunctExtender* (GB9c). One forward pass,
// no backtracking, no allocation: O(n) for every input. Width takes ASCII
// text 16 bytes a vector (std.simd at the 16-lane baseline, not compiled on
// x86_64-freestanding); Clusters.Next returns one cluster a call, so its
// ASCII path stays the two-byte test.

import std.raw
import std.simd

// The Grapheme_Cluster_Break values of a Prop byte's low four bits.
const GcbOther: u8 = 0
const GcbCR: u8 = 1
const GcbLF: u8 = 2
const GcbControl: u8 = 3
const GcbExtend: u8 = 4
const GcbZWJ: u8 = 5
const GcbRI: u8 = 6
const GcbPrepend: u8 = 7
const GcbSpacingMark: u8 = 8
const GcbL: u8 = 9
const GcbV: u8 = 10
const GcbT: u8 = 11
const GcbLV: u8 = 12
const GcbLVT: u8 = 13

// The Indic_Conjunct_Break values of a Prop byte's bits 5 and 6.
const InCBLinker: u8 = 1
const InCBConsonant: u8 = 2
const InCBExtend: u8 = 3

const PropExtPict: u8 = 0x10
const PropWide: u8 = 0x80

// Bit q of NoBreak[p] is set when GB3 to GB9b join a scalar of class p
// to a following one of class q (GB999 breaks every pair left clear).
const Ext: u16 = (1 << 4) | (1 << 5) | (1 << 8)
const NoBreak: [14]u16 = [
    Ext,                                                // Other
    1 << 2,                                             // CR: GB3
    0,                                                  // LF: GB4
    0,                                                  // Control: GB4
    Ext,                                                // Extend
    Ext,                                                // ZWJ
    Ext,                                                // RI
    0x3FFF & ~((1 << 1) | (1 << 2) | (1 << 3)),         // Prepend: GB9b, but GB5
    Ext,                                                // SpacingMark
    Ext | (1 << 9) | (1 << 10) | (1 << 12) | (1 << 13), // L: GB6
    Ext | (1 << 10) | (1 << 11),                        // V: GB7
    Ext | (1 << 11),                                    // T: GB8
    Ext | (1 << 10) | (1 << 11),                        // LV: GB7
    Ext | (1 << 11),                                    // LVT: GB8
]

/// An iterator over grapheme clusters, from text.Graphemes: call Next
/// until none.
export view struct Clusters {
    text: str
    pos: int
}

/// The grapheme clusters of s in order: what a reader sees as one
/// character, such as "e" with a combining accent, a flag or a family
/// emoji. These are UAX 29's extended grapheme clusters. "" yields none.
/// O(1); allocates nothing.
export Graphemes(s: str): Clusters {
    return Clusters(text: s, pos: 0)
}

/// The next cluster, a view of the text; none after the last one. An ASCII
/// byte other than CR, followed by another ASCII byte or by the end, is a
/// cluster by itself. O(cluster length); allocates nothing.
export Clusters.Next(mut self): ?str from self.text {
    b := self.text.AsBytes()
    n := b.Len()
    i := self.pos
    if i >= n {
        return none
    }
    c := b[i]
    if c < 0x80 && c != 0x0D && (i + 1 == n || b[i + 1] < 0x80) {
        self.pos = i + 1
        return some(self.text[i..i + 1])
    }
    end := ClusterEnd(b, i)
    self.pos = end
    return some(self.text[i..end])
}

// ClusterEnd is the offset after the cluster that starts at b[i], i < n.
ClusterEnd(b: []u8, i: int): int {
    n := b.Len()
    first := Decode(b, i)
    var p := PropOf(first.c)
    var j := i + first.n
    // emoji: 1 after ExtPict Extend*, 2 after ExtPict Extend* ZWJ (GB11).
    var emoji := if p & PropExtPict != 0 { 1 } else { 0 }
    // ri: the regional indicators ending at p (GB12, GB13).
    var ri := if p & 15 == GcbRI { 1 } else { 0 }
    // linked: after ConjunctLinker ConjunctExtender* (GB9c).
    var linked := (p >> 5) & 3 == InCBLinker
    tab := &NoBreak
    for j < n {
        r := Decode(b, j)
        q := PropOf(r.c)
        pg := p & 15
        qg := q & 15
        if (tab[pg] >> qg) & 1 == 0 {
            // GB4 and GB5 are settled: no flag is set after a control.
            if !(linked && (q >> 5) & 3 == InCBConsonant) && !(emoji == 2 && q & PropExtPict != 0) && !(qg == GcbRI && ri & 1 == 1) {
                break
            }
        }
        incb := (q >> 5) & 3
        linked = if incb == InCBLinker { true } else if incb == InCBExtend { linked } else { false }
        emoji = if q & PropExtPict != 0 || emoji == 1 && qg == GcbExtend {
            1
        } else if emoji == 1 && qg == GcbZWJ {
            2
        } else {
            0
        }
        ri = if qg == GcbRI { ri + 1 } else { 0 }
        p = q
        j += r.n
    }
    return j
}

/// The number of terminal columns s takes: the sum of its grapheme
/// clusters' widths. A cluster is as wide as its first scalar: 0 for a
/// control, CR, LF, a combining mark (Extend) and ZWJ; 2 for a wide or
/// full-width scalar (East_Asian_Width W or F), an emoji shown as emoji
/// (Emoji_Presentation) and a regional indicator; 1 for the rest. A
/// cluster led by a pictograph (Extended_Pictographic) is 2 when U+FE0F
/// follows it and 1 when U+FE0E does. O(n); allocates nothing.
export Width(s: str): int {
    b := s.AsBytes()
    n := b.Len()
    var w := 0
    var i := 0
    for i < n {
        c := b[i]
        if Target.os != Os.Freestanding && c < 0x80 && i + 16 <= n {
            // The first t bytes of 16, each ASCII and followed by an ASCII
            // byte (or the end), are t clusters of one byte each but for a
            // CR LF pair, which is one of width 0 like its two bytes (also
            // when the LF is past t): their width is the count of printable
            // lanes among them. The byte before the first non-ASCII one is
            // left to the cluster scan, since marks may follow it.
            // The all-ASCII step adds a constant, so the next load does not
            // wait on this one's mask.
            v := simd.Vec[u8, 16].FromSlice(b, i)
            hi := (v >= simd.Vec[u8, 16].Splat(0x80)).Bits()
            pr := ((v >= simd.Vec[u8, 16].Splat(0x20)) & (v < simd.Vec[u8, 16].Splat(0x7F))).Bits()
            if hi == 0 && (i + 16 == n || b[i + 16] < 0x80) {
                w += raw.Popcount64(pr)
                i += 16
                continue
            }
            t := if hi != 0 { raw.Ctz64(hi) - 1 } else { 15 }
            if t > 0 {
                w += raw.Popcount64(pr & ((Cast[u64](1) << t) - 1))
                i += t
                continue
            }
        }
        if c < 0x80 && c != 0x0D && (i + 1 == n || b[i + 1] < 0x80) {
            if c >= 0x20 && c < 0x7F {
                w += 1
            }
            i += 1
            continue
        }
        end := ClusterEnd(b, i)
        w += ClusterWidth(b, i, end)
        i = end
    }
    return w
}

// ClusterWidth is the width of the cluster b[i..end].
ClusterWidth(b: []u8, i: int, end: int): int {
    first := Decode(b, i)
    p := PropOf(first.c)
    g := p & 15
    if g >= GcbCR && g <= GcbZWJ {
        return 0
    }
    w := if p & PropWide != 0 || g == GcbRI { 2 } else { 1 }
    if p & PropExtPict != 0 {
        // A variation sequence: U+FE0F (EF B8 8F) or U+FE0E (EF B8 8E)
        // right after the first scalar.
        k := i + first.n
        if k + 2 < end && b[k] == 0xEF && b[k + 1] == 0xB8 {
            if b[k + 2] == 0x8F {
                return 2
            }
            if b[k + 2] == 0x8E {
                return 1
            }
        }
    }
    return w
}
