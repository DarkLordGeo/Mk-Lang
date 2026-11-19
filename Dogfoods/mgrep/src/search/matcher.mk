// Why: most searches are plain words; they should not pay for a regex.
// What: Spec, Matcher, Compile, and the BadPattern and PatternTooLarge
// errors.
// How: a single pattern with no regex syntax becomes a literal search and a
// bracket class of plain bytes a byte-class search, recognised by matchers
// that `gen regex.Matcher` builds at compile time; anything else compiles
// to a std.regex program. A vector byte search skips lines that cannot match.

import std.regex
import std.simd

/// How a compiled pattern finds its matches.
export enum Mode {
    /// A fixed string, found by its first byte with simd.IndexByte, or by both cases of it with simd.IndexAny
    Literal
    /// One byte of a bracket class of plain ASCII bytes, found with simd.IndexAny
    ByteClass
    /// A regular expression, matched line by line
    Pattern
}

/// A pattern that does not compile, with the index of the pattern among those given and the byte offset in it.
export error BadPattern(index: int, offset: int)

/// The patterns together compile past the regex engine's limits.
export error PatternTooLarge

export type Error = BadPattern | PatternTooLarge | OutOfMemory

/// What is searched for and how.
export struct Spec {
    /// The patterns; more than one matches when any of them does
    export patterns: []str
    /// Match the patterns as fixed strings
    export fixed: bool
    /// Ignore ASCII case in fixed strings, and simple case folding in a regex
    export fold: bool
    /// Match only at word boundaries
    export word: bool
}

/// A pattern compiled once and read by every search task.
export view struct Matcher {
    export mode: Mode
    needle: []u8
    fold: bool
    word: bool
    set: []u8
    re: ?regex.Regex
}

// A pattern written as a bracket class of plain ASCII bytes, such as `[,;:]`
struct SimpleClass {
}

gen regex.Matcher(SimpleClass, r"^\[[^\]\[\\^-]+\]$")

// A byte that means something in a regex
struct MetaByte {
}

gen regex.Matcher(MetaByte, r"[\\.+*?()|\[\]{}^$]")

/// Compiles `spec` into a matcher. A single pattern with no regex syntax becomes
/// a literal search, and a bracket class of plain bytes a byte-class search;
/// anything else compiles to a regex. Fails with `BadPattern` naming the pattern
/// and offset that do not parse, and `PatternTooLarge` past the engine's size.
/// O(pattern length); allocates the needle or the compiled program in `a`.
export Compile(a: &Arena, spec: &Spec): Error!Matcher from a {
    if spec.patterns.Len() == 1 && spec.patterns[0].Len() > 0 {
        p := spec.patterns[0]
        if !spec.fixed && !spec.fold && !spec.word && IsAscii(p) && SimpleClass.IsMatch(p) {
            set := try a.Dup(p[1..p.Len() - 1])
            return Matcher(mode: Mode.ByteClass, needle: set.AsBytes(), fold: false, word: false, set: set.AsBytes(), re: none)
        }
        if (spec.fixed || !MetaByte.IsMatch(p)) && (!spec.fold || IsAscii(p)) {
            return try Literal(a, p, spec.fold, spec.word)
        }
    }
    if !spec.fixed {
        // each pattern alone first, so an unclosed group is reported in the
        // user's text and not at the group that joins the patterns
        for i, p in spec.patterns {
            _ = regex.Compile(a, p) else e {
                fail Translate(e, i)
            }
        }
    }
    var text := Text.New(a)
    if spec.fold {
        try text.Push("(?i)")
    }
    if spec.word {
        try text.Push(r"\b(?:")
    }
    var starts := List[int].New(a)
    for i, p in spec.patterns {
        if i > 0 {
            try text.Push("|")
        }
        try text.Push("(?:")
        try starts.Push(text.Str().Len())
        if spec.fixed {
            try PushEscaped(mut text, p)
        } else {
            try text.Push(p)
        }
        try text.Push(")")
    }
    if spec.word {
        try text.Push(r")\b")
    }
    re := regex.Compile(a, text.Str()) else e {
        if bad := Locate(spec.patterns, starts[..], e) {
            fail bad
        }
        fail Translate(e, 0)
    }
    return Matcher(mode: Mode.Pattern, needle: b"", fold: spec.fold, word: spec.word, set: b"", re: some(re))
}

// The error of a regex that does not compile, as mgrep reports it: a syntax
// error names the pattern `index` and the offset in it.
Translate(e: regex.Error, index: int): Error {
    match e {
        regex.Syntax(offset) {
            return BadPattern(index: index, offset: offset)
        }
        regex.TooLarge {
            return PatternTooLarge
        }
        OutOfMemory {
            return OutOfMemory
        }
        _ {
            Panic("regex.Error gained a member this match does not handle")
        }
    }
}

// Maps a syntax error in the joined regex back to the pattern it falls in;
// none for any other error.
Locate(patterns: []str, starts: []int, e: regex.Error): ?BadPattern {
    offset := match e {
        regex.Syntax(offset) { offset }
        _ { return none }
    }
    var index := 0
    for i, s in starts {
        if s <= offset {
            index = i
        }
    }
    rel := offset - starts[index]
    limit := patterns[index].Len()
    at := if rel < 0 { 0 } else if rel > limit { limit } else { rel }
    return some(BadPattern(index: index, offset: at))
}

Literal(a: &Arena, p: str, fold: bool, word: bool): OutOfMemory!Matcher from a {
    if !fold {
        needle := try a.Dup(p)
        return Matcher(mode: Mode.Literal, needle: needle.AsBytes(), fold: false, word: word, set: b"", re: none)
    }
    lower := try p.ToLower(a)
    first := lower.Byte(0)
    var set := try a.Slice[u8](2)
    set[0] = first
    set[1] = Upper(first)
    return Matcher(mode: Mode.Literal, needle: lower.AsBytes(), fold: true, word: word, set: set[..], re: none)
}

// Writes `p` with every regex meta byte escaped.
PushEscaped(text: mut Text, p: str): OutOfMemory!void {
    var from := 0
    for i in 0..p.Len() {
        if MetaByte.IsMatch(p[i..i + 1]) {
            try text.Push(p[from..i])
            try text.Push(r"\")
            from = i
        }
    }
    try text.Push(p[from..])
}

/// True when every byte of `s` is ASCII.
export IsAscii(s: str): bool {
    for b in s.AsBytes() {
        if b >= 0x80 {
            return false
        }
    }
    return true
}

Lower(b: u8): u8 {
    return if b >= b'A' && b <= b'Z' { b + 32 } else { b }
}

Upper(b: u8): u8 {
    return if b >= b'a' && b <= b'z' { b - 32 } else { b }
}

IsWordByte(b: u8): bool {
    return (b >= b'a' && b <= b'z') || (b >= b'A' && b <= b'Z') || (b >= b'0' && b <= b'9') || b == b'_'
}

/// The first match in `line` that starts at byte `from` or later. For a regex
/// the line is matched whole, so `^` and `\b` keep their meaning, and a match
/// that starts before `from` is skipped. O(line length).
export Matcher.Find(&self, line: str, from: int): ?regex.Match {
    match self.mode {
        .Literal {
            return self.FindLiteral(line.AsBytes(), from)
        }
        .ByteClass {
            i := simd.IndexAny(line.AsBytes()[from..], self.set) else {
                return none
            }
            return some(regex.Match(lo: from + i, hi: from + i + 1))
        }
        .Pattern {
            match &self.re {
                some(re) {
                    m := re.Find(line) else {
                        return none
                    }
                    if m.lo >= from {
                        return some(m)
                    }
                    return none
                }
                none {
                    return none
                }
            }
        }
    }
}

/// Reports whether `line` holds a match. O(line length).
export Matcher.IsMatch(&self, line: str): bool {
    if self.mode == Mode.Pattern {
        match &self.re {
            some(re) {
                return re.IsMatch(line)
            }
            none {
                return false
            }
        }
    }
    return self.Find(line, 0).IsSome()
}

/// Every non-overlapping match in `line`, leftmost first. O(line length);
/// allocates the list in `a`.
export Matcher.All(&self, a: &Arena, line: str): OutOfMemory!List[regex.Match] from a {
    if self.mode == Mode.Pattern {
        match &self.re {
            some(re) {
                return try re.FindAll(a, line)
            }
            none {
                return List[regex.Match].New(a)
            }
        }
    }
    var out := List[regex.Match].New(a)
    var from := 0
    for from <= line.Len() {
        m := self.Find(line, from) else {
            break
        }
        try out.Push(m)
        from = m.hi
    }
    return out
}

/// Where the next line that may match starts its match, searching `buf` from
/// byte `from` with a vector byte search; `none` means no later line matches.
/// A regex has no such filter and answers `from`. O(bytes skipped).
export Matcher.Candidate(&self, buf: []u8, from: int): ?int {
    match self.mode {
        .Literal {
            found := if self.fold { simd.IndexAny(buf[from..], self.set) } else { simd.IndexByte(buf[from..], self.needle[0]) }
            i := found else {
                return none
            }
            return some(from + i)
        }
        .ByteClass {
            i := simd.IndexAny(buf[from..], self.set) else {
                return none
            }
            return some(from + i)
        }
        .Pattern {
            return some(from)
        }
    }
}

/// The one byte a literal search looks for, when every match is that byte
/// alone, so matches can be counted with simd.Count.
export Matcher.SingleByte(&self): ?u8 {
    if self.mode == Mode.Literal && self.needle.Len() == 1 && !self.fold && !self.word {
        return some(self.needle[0])
    }
    return none
}

/// The compiled regex, for replacement and groups; `none` for the literal modes.
export Matcher.Regex(&self): ?&regex.Regex {
    match &self.re {
        some(re) {
            return some(re)
        }
        none {
            return none
        }
    }
}

Matcher.FindLiteral(&self, s: []u8, from: int): ?regex.Match {
    n := self.needle.Len()
    var i := from
    for i + n <= s.Len() {
        found := if self.fold { simd.IndexAny(s[i..], self.set) } else { simd.IndexByte(s[i..], self.needle[0]) }
        j := found else {
            return none
        }
        at := i + j
        if at + n > s.Len() {
            return none
        }
        if self.SameAt(s, at) && self.Bounded(s, at, at + n) {
            return some(regex.Match(lo: at, hi: at + n))
        }
        i = at + 1
    }
    return none
}

Matcher.SameAt(&self, s: []u8, at: int): bool {
    if !self.fold {
        return simd.Equal(s[at..at + self.needle.Len()], self.needle)
    }
    for k, b in self.needle {
        if Lower(s[at + k]) != b {
            return false
        }
    }
    return true
}

Matcher.Bounded(&self, s: []u8, lo: int, hi: int): bool {
    if !self.word {
        return true
    }
    if lo > 0 && IsWordByte(s[lo - 1]) {
        return false
    }
    return hi == s.Len() || !IsWordByte(s[hi])
}
