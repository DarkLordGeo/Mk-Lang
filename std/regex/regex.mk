// Why: programs need regular expressions whose matching time is linear in
// the input whatever the pattern, so a pattern from outside cannot stall
// them.
//
// What: Regex with Compile, IsMatch, Find, Group, FindAll and Replace, Match,
// and the errors Syntax and TooLarge: RE2's syntax without back-references,
// matched leftmost-first.
//
// How: Compile parses (parse.mk), compiles (compile.mk) and builds the DFAs
// in a sub-arena, and keeps their results in the arena; the matching calls
// run the DFAs (dfa.mk) or the Pike VM (pike.mk). IsMatch, Find and Group
// allocate nothing, FindAll and Replace only in their arena (Replace with
// capture.mk once per call), and none writes anything but its own frame
// and that arena, so a Regex is Share.

import std.simd

/// The pattern is not valid syntax; offset is the pattern byte where the
/// error is found.
export error Syntax(offset: int)

/// The pattern nests more than 1,000 levels, or its program passes 4,096
/// instructions.
export error TooLarge

/// Every way Compile fails.
export open type Error = Syntax | TooLarge | OutOfMemory

/// A match: the byte offsets lo to hi of s, on scalar boundaries; the
/// matched text is s[lo..hi].
export struct Match {
    /// The byte offset of the match's start in s.
    export lo: int
    /// The byte offset where the match ends in s, exclusive.
    export hi: int
}

/// Regex is a compiled pattern. It lives in the arena Compile was given,
/// and matching writes nothing but its own frame and the arena a call is
/// given, so one Regex can be used by many tasks at once (it is Share).
export view struct Regex {
    insts: []Inst
    edges: []Edge
    names: []str
    groups: int
    start: u32
    anchor: bool
    dfa: Dfa
    rdfa: Dfa
    prefix: str
    pair: PrePair
}

/// Compiles pattern into a Regex in a. The syntax is RE2's without
/// back-references: Perl classes, \p{..} Unicode classes, named groups,
/// lazy repeats and the flags i, m, s and U; matching is leftmost-first.
/// Fails with Syntax at the first bad byte, TooLarge past 1,000 levels of
/// nesting or 4,096 instructions, or OutOfMemory.
///
/// The forward and reverse DFAs are built at once, under a budget of 4,096
/// states and of build work each; past the budget, matching runs the Pike
/// VM instead.
/// O(pattern) to parse and compile, O(states x 256) per DFA. Keeps in a
/// only what matching reads: the program and each DFA's tables at their
/// exact sizes (at most 4096 x 257 cells of 4 bytes, 4,112 KiB, a DFA),
/// the group names and the literal prefix. The parse tree and the build's
/// scratch go in a sub-arena of a, freed before it returns.
export Compile(a: &Arena, pattern: str): Error!Regex from a {
    return try CompileIn(a, pattern, MaxDfaStates)
}

// CompileIn is Compile with a budget of max states (a power of two) per DFA.
CompileIn(a: &Arena, pattern: str, max: int): Error!Regex from a {
    var s := try a.Sub()
    defer s.Free()
    t := try ParseTree(&s, pattern)
    p := try CompileTree(&s, t, false)
    anchor := Anchored(&t, t.root)
    d := try BuildDfa(&s, p.insts, p.edges, p.start, anchor, false, max)
    var off := d
    off.ok = false
    r := if d.ok { try RevDfa(&s, t, off, max) } else { off }
    pre := if anchor { "" } else { try a.Dup(try Prefix(&s, &t)) }
    insts := try a.Slice[Inst](p.insts.Len())
    insts.CopyFrom(p.insts)
    edges := try a.Slice[Edge](p.edges.Len())
    edges.CopyFrom(p.edges)
    return Regex(insts: insts[..], edges: edges[..], names: try KeptNames(a, t.names), groups: t.groups, start: p.start, anchor: anchor, dfa: try KeptDfa(a, d), rdfa: try KeptDfa(a, r), prefix: pre, pair: PairOf(pre))
}

// KeptNames is a copy of names in a, each name's bytes included.
KeptNames(a: &Arena, names: []str): OutOfMemory![]str from a {
    var out := List[str].New(a)
    for name in names {
        try out.Push(try a.Dup(name))
    }
    return out.Done()[..]
}

// KeptDfa is d with its tables copied into a at their exact sizes. A DFA
// past its budget is never scanned (Next, IsMatch and gen check ok
// first), so it keeps no table.
KeptDfa(a: &Arena, d: Dfa): OutOfMemory!Dfa from a {
    nc := if d.ok { d.cls.Len() } else { 0 }
    nt := if d.ok { d.trans.Len() } else { 0 }
    cls := try a.Slice[u8](nc)
    trans := try a.Slice[u32](nt)
    if d.ok {
        cls.CopyFrom(d.cls)
        trans.CopyFrom(d.trans)
    }
    return Dfa(ok: d.ok, cls: cls[..], trans: trans[..], starts: d.starts, eot: d.eot, stride: d.stride, top: d.top)
}

// RevDfa is the reverse DFA of t, or off when the reversed program is
// over MaxInsts, which costs only the reverse DFA.
RevDfa(a: &Arena, t: Tree, off: Dfa, max: int): OutOfMemory!Dfa from a, t, off {
    q := CompileTree(a, t, true) else e {
        match e {
            Syntax(offset) {
                _ = offset
                return off
            }
            TooLarge {
                return off
            }
            OutOfMemory {
                fail OutOfMemory
            }
        }
    }
    return try BuildDfa(a, q.insts, q.edges, q.start, false, true, max)
}

// Prefix is the text every match of t begins with: the literals that
// start its top concatenation, through groups, as UTF-8 in a ("" when the
// pattern starts with anything else). Under (?i) a letter is a class, so
// it ends the prefix.
Prefix(a: &Arena, t: &Tree): OutOfMemory!str from a {
    var buf := List[u8].New(a)
    _ = try PrefixOf(t, t.root, mut buf)
    // PrefixOf appends whole scalars' UTF-8 only, so buf is UTF-8.
    return str.From(buf.Done()[..]).Must()
}

// PrefixOf adds node i's leading literals to buf; true when all of node i
// is literal, so the prefix goes on after it.
PrefixOf(t: &Tree, i: int, buf: mut List[u8]): OutOfMemory!bool {
    nd := t.nodes[i]
    if nd.kind == KLit {
        c := Cast[u32](nd.a)
        if c >= 0xD800 && c <= 0xDFFF {
            return false
        }
        e := Encode(c)
        for k in 0..e.n {
            try buf.Push(e.b[k])
        }
        return true
    }
    if nd.kind == KCap {
        return try PrefixOf(t, nd.a, buf)
    }
    if nd.kind == KCat {
        for k in nd.a..nd.a + nd.b {
            if !try PrefixOf(t, t.kids[k], buf) {
                return false
            }
        }
        return true
    }
    return false
}

// Next is the leftmost-first match at or after pos. With both DFAs, the
// forward scan finds its end and the reverse scan from there its start;
// with the forward DFA alone, it says whether there is one and the Pike VM
// finds it; with neither, the Pike VM alone.
Regex.Next(&self, s: str, pos: int): ?Hit {
    b := s.AsBytes()
    if self.dfa.ok {
        if self.rdfa.ok {
            e := Scan(&self.dfa, s, pos, false, self.prefix, self.pair)
            if e < 0 {
                return none
            }
            return some(Hit(lo: ScanRev(&self.rdfa, b, pos, e), hi: e, g0: -1, g1: -1))
        }
        if Scan(&self.dfa, s, pos, true, self.prefix, self.pair) < 0 {
            return none
        }
    }
    return Pike(self, b, pos, false, 0, false)
}

// Anchored: every match of node i starts at text begin.
Anchored(t: &Tree, i: int): bool {
    nd := t.nodes[i]
    if nd.kind == KAssert {
        return Cast[u8](nd.a) == ABeginText
    }
    if nd.kind == KCat {
        return nd.b > 0 && Anchored(t, t.kids[nd.a])
    }
    if nd.kind == KCap {
        return Anchored(t, nd.a)
    }
    return false
}

/// Reports whether some substring of s matches. O(n) with the DFAs, O(n x
/// program) with the Pike VM; allocates nothing.
export Regex.IsMatch(&self, s: str): bool {
    if self.dfa.ok {
        return Scan(&self.dfa, s, 0, true, self.prefix, self.pair) >= 0
    }
    return Pike(self, s.AsBytes(), 0, false, 0, true).IsSome()
}

/// Find gives the leftmost-first match, or none. O(n) with the DFAs, O(n x
/// program) with the Pike VM; allocates nothing.
export Regex.Find(&self, s: str): ?Match {
    h := self.Next(s, 0) else {
        return none
    }
    return some(Match(lo: h.lo, hi: h.hi))
}

/// FindAll gives the successive non-overlapping matches, each search
/// starting where the last match ended. An empty match right after a match
/// is skipped, and an empty match moves the next search on by one scalar.
/// Fails only with OutOfMemory. O(n) per match found with the DFAs;
/// allocates the List in a.
export Regex.FindAll(&self, a: &Arena, s: str): OutOfMemory!List[Match] from a {
    var out := List[Match].New(a)
    b := s.AsBytes()
    n := b.Len()
    var pos := 0
    var prev := -1
    for pos <= n {
        h := self.Next(s, pos) else {
            break
        }
        accept := h.hi != pos || h.lo != prev
        pos = if h.hi == pos { NextScalar(b, pos) } else { h.hi }
        prev = h.hi
        if accept {
            try out.Push(Match(lo: h.lo, hi: h.hi))
        }
    }
    return out
}

/// Group gives group i's text in the match m of s: i == 0 is the match
/// itself; for a group, the text it took in the leftmost-first match that
/// starts at m.lo, if that match ends at m.hi. none for a group that took
/// no part, for an i that names no group, or for an m outside s. One
/// anchored run over the match; allocates nothing.
export Regex.Group(&self, s: str, m: Match, i: int): ?str from s {
    g := self.Span(s.AsBytes(), m, i) else {
        return none
    }
    return some(s[g.lo..g.hi])
}

// Span is Group's offsets.
Regex.Span(&self, b: []u8, m: Match, i: int): ?Match {
    if m.lo < 0 || m.lo > m.hi || m.hi > b.Len() || !Boundary(b, m.lo) || !Boundary(b, m.hi) {
        return none
    }
    if i == 0 {
        return some(m)
    }
    if i < 0 || i > self.groups {
        return none
    }
    // The backtracker on the match's bytes alone (backtrack.mk), else the
    // Pike VM, which may read on to the end of b.
    bt := Back(self, b, m.lo, m.hi, i)
    if bt.state == BtNone {
        return none
    }
    if bt.state == BtFound {
        if bt.hi != m.hi || bt.g0 < 0 || bt.g1 < 0 {
            return none
        }
        return some(Match(lo: bt.g0, hi: bt.g1))
    }
    h := Pike(self, b, m.lo, true, i, false) else {
        return none
    }
    if h.hi != m.hi || h.g0 < 0 || h.g1 < 0 {
        return none
    }
    return some(Match(lo: h.g0, hi: h.g1))
}

Boundary(b: []u8, i: int): bool {
    return i == b.Len() || b[i] & 0xC0 != 0x80
}

// NextScalar is the offset after the scalar at i, or n + 1 at the end.
NextScalar(b: []u8, i: int): int {
    if i >= b.Len() {
        return b.Len() + 1
    }
    return i + Width(b[i])
}

// Width is the length of the UTF-8 sequence that lead byte c starts.
Width(c: u8): int {
    if c < 0x80 {
        return 1
    }
    if c < 0xE0 {
        return 2
    }
    if c < 0xF0 {
        return 3
    }
    return 4
}

// Piece is a run of bytes of the output: from s (src 0) or with (1).
struct Piece {
    src: u8
    lo: int
    hi: int
}

/// Replace is s with every match replaced by with, in which $1, ${1},
/// $name and ${name} give a group's text (nothing for a group that took no
/// part or does not exist), and $$ is $. In $name the name is the longest
/// run of letters, decimal digits and _, of any script; write ${name} to
/// end it early. The result is a new str in a, also when nothing matched.
/// Fails only with OutOfMemory. FindAll's cost plus one capture run per
/// match; allocates the result and the capture rows in a.
export Regex.Replace(&self, a: &Arena, s: str, with: str): OutOfMemory!str from a {
    b := s.AsBytes()
    n := b.Len()
    var parts := List[Piece].New(a)
    // One pass with every slot per match, only when the template names a
    // group (capture.mk); the scratch is made once, here.
    var caps := try NewCaps(a, self, HasRef(with.AsBytes()))
    var last := 0
    var pos := 0
    for pos <= n {
        h := self.Next(s, pos) else {
            break
        }
        try parts.Push(Piece(src: 0, lo: last, hi: h.lo))
        if h.hi > last || h.lo == 0 {
            ok := if caps.w > 0 { try caps.Run(self, b, h.lo) && caps.best[1] == h.hi } else { false }
            try self.Expand(mut parts, b, with.AsBytes(), Match(lo: h.lo, hi: h.hi), &caps, ok)
        }
        last = h.hi
        // Go's replaceAll: always move on by at least one scalar.
        w := if pos < n { Width(b[pos]) } else { 0 }
        pos = if pos + w > h.hi { pos + w } else if pos + 1 > h.hi { pos + 1 } else { h.hi }
    }
    try parts.Push(Piece(src: 0, lo: last, hi: n))
    wb := with.AsBytes()
    var total := 0
    for p in &parts {
        total += p.hi - p.lo
    }
    buf := try a.Slice[u8](total)
    var o := 0
    for p in &parts {
        k := p.hi - p.lo
        src := if p.src == 0 { b } else { wb }
        (mut buf[o..o + k]).CopyFrom(src[p.lo..p.hi])
        o += k
    }
    // Every piece is a run of s or of with that starts and ends on a scalar
    // boundary (a match does, and so does a group), so buf is UTF-8.
    return str.From(buf[..]).Must()
}

// HasRef: template w names a group, so Replace needs the slots.
HasRef(w: []u8): bool {
    var i := 0
    for i < w.Len() {
        if w[i] != b'$' {
            i += 1
            continue
        }
        i += 1
        if i < w.Len() && w[i] == b'$' {
            i += 1
            continue
        }
        if Extract(w, i).ok {
            return true
        }
    }
    return false
}

// Ref is group k's span in match m of b: from caps when ok (one pass made
// them all), else from a pass of its own.
Regex.Ref(&self, b: []u8, m: Match, k: int, caps: &Caps, ok: bool): ?Match {
    if ok {
        return caps.Got(k)
    }
    return self.Span(b, m, k)
}

// Expand adds the pieces of template w for match m of b, as Go's expand.
Regex.Expand(&self, parts: mut List[Piece], b: []u8, w: []u8, m: Match, caps: &Caps, ok: bool): OutOfMemory!void {
    var lit := 0
    var i := 0
    for i < w.Len() {
        if w[i] != b'$' {
            i += 1
            continue
        }
        try parts.Push(Piece(src: 1, lo: lit, hi: i))
        i += 1
        if i < w.Len() && w[i] == b'$' {
            try parts.Push(Piece(src: 1, lo: i, hi: i + 1))
            i += 1
            lit = i
            continue
        }
        e := Extract(w, i)
        if !e.ok {
            // Not a reference: the $ stays as text.
            lit = i - 1
            continue
        }
        i = e.rest
        lit = i
        if e.num >= 0 {
            if g := self.Ref(b, m, e.num, caps, ok) {
                try parts.Push(Piece(src: 0, lo: g.lo, hi: g.hi))
            }
            continue
        }
        for k in 1..self.groups + 1 {
            if simd.Equal(self.names[k].AsBytes(), w[e.lo..e.hi]) {
                if g := self.Ref(b, m, k, caps, ok) {
                    try parts.Push(Piece(src: 0, lo: g.lo, hi: g.hi))
                    break
                }
            }
        }
    }
    try parts.Push(Piece(src: 1, lo: lit, hi: w.Len()))
}

// NameScalar is the length of the scalar at j of template w when it can
// be part of a reference name, else 0: a letter (general category L), a
// decimal digit (Nd) or _, as Go's extract reads it with unicode.IsLetter
// and unicode.IsDigit (an ASCII-only name would make `$1é` group 1 and then
// é, where Go reads the name `1é`). ASCII is decided by Word; any
// other scalar by binary search of the L and Nd tables.
NameScalar(w: []u8, j: int): int {
    c := w[j]
    if c < 0x80 {
        return if Word(c) { 1 } else { 0 }
    }
    d := Decode(w, j)
    if InUcd(UcdLetter, d.c) || InUcd(UcdDigit, d.c) {
        return d.n
    }
    return 0
}

// Extract reads a reference name at i of w (after the $), as Go's
// extract: name or {name}, the name lo .. hi; num is its value when it is
// all digits without a leading zero and below 10^8, else -1; rest is
// where the template goes on.
Extract(w: []u8, i: int): (ok: bool, lo: int, hi: int, num: int, rest: int) {
    no := (ok: false, lo: 0, hi: 0, num: -1, rest: i)
    brace := i < w.Len() && w[i] == b'{'
    var j := if brace { i + 1 } else { i }
    lo := j
    for j < w.Len() {
        n := NameScalar(w, j)
        if n == 0 {
            break
        }
        j += n
    }
    if j == lo {
        return no
    }
    hi := j
    if brace {
        if j >= w.Len() || w[j] != b'}' {
            return no
        }
        j += 1
    }
    var num := 0
    for k in lo..hi {
        c := w[k]
        if c < b'0' || c > b'9' || num >= 100_000_000 {
            num = -1
            break
        }
        num = num * 10 + Cast[int](c - b'0')
    }
    if w[lo] == b'0' && hi - lo > 1 {
        num = -1
    }
    return (ok: true, lo: lo, hi: hi, num: num, rest: j)
}
