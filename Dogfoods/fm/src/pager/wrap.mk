// Why: the pager shows text files of any width on a terminal of fixed width.
// What: Wrap and its Wrapper iterator of Spans, WrapAll and TrimEnd.
// How: breaks at the last word boundary that fits, else between grapheme
// clusters, counting a wide character as two columns; a Span is byte
// offsets into the source, so nothing is copied.

import std.text

/// One display line of wrapped text: the bytes `start` to `end` of the
/// text it was wrapped from.
export struct Span {
    export start: int
    export end: int
}

/// Breaks text into display lines no wider than `width` terminal columns.
/// Each source line is broken at UAX 29 word boundaries (`text.WordBounds`)
/// and measured with `text.Width`; a word wider than the line is broken
/// between grapheme clusters, and the spaces at a break are dropped. An
/// empty source line is an empty display line, and a final newline starts
/// no line. Iterate it with `for s in pager.Wrap(text, width)`.
export view struct Wrapper {
    src: str
    width: int
    pos: int
    done: bool
}

/// Starts wrapping `src` to `width` columns; a width below 1 counts as 1.
/// O(1); allocates nothing.
export Wrap(src: str, width: int): Wrapper {
    w := if width < 1 { 1 } else { width }
    return Wrapper(src: src, width: w, pos: 0, done: src.Len() == 0)
}

/// The next display line. O(length of its source line) per call;
/// allocates nothing.
export Wrapper.Next(mut self): ?Span {
    if self.done {
        return none
    }
    rest := self.src[self.pos..]
    var line_end := rest.Len()
    var next := rest.Len()
    if nl := rest.Find("\n") {
        line_end = nl
        next = nl + 1
    }
    var line := rest[..line_end]
    if line.EndsWith("\r") {
        line = line[..line.Len() - 1]
    }
    if text.Width(line) <= self.width {
        start := self.pos
        self.pos += next
        if self.pos >= self.src.Len() {
            self.done = true
        }
        return some(Span(start: start, end: start + line.Len()))
    }
    (keep, skip) := Break(line, self.width)
    start := self.pos
    self.pos += skip
    return some(Span(start: start, end: start + TrimEnd(line[..keep]).Len()))
}

// Where the first display line of `line` ends (`keep`) and where the next
// one starts (`skip`), for a line wider than `width`.
Break(line: str, width: int): (keep: int, skip: int) {
    var b := text.WordBounds(line)
    var prev := 0
    var used := 0
    for end := b.Next() {
        if end == 0 {
            continue
        }
        seg := line[prev..end]
        w := text.Width(seg)
        if used + w <= width {
            used += w
            prev = end
            continue
        }
        if IsBlank(seg) {
            return (keep: prev, skip: end)
        }
        if prev > 0 {
            return (keep: prev, skip: prev)
        }
        cut := Fit(seg, width)
        return (keep: cut, skip: cut)
    }
    return (keep: line.Len(), skip: line.Len())
}

// The bytes of the grapheme clusters at the start of `s` that fit in
// `width` columns, at least one cluster so the wrap always moves on.
Fit(s: str, width: int): int {
    var used := 0
    var n := 0
    for c in text.Graphemes(s) {
        w := text.Width(c)
        if n > 0 && used + w > width {
            break
        }
        used += w
        n += c.Len()
    }
    return n
}

IsBlank(s: str): bool {
    for c in s.Scalars() {
        if c != ' ' && c != '\t' {
            return false
        }
    }
    return true
}

/// `s` without trailing spaces and tabs. O(n); allocates nothing.
export TrimEnd(s: str): str {
    var end := s.Len()
    for end > 0 && (s.Byte(end - 1) == b' ' || s.Byte(end - 1) == b'\t') {
        end -= 1
    }
    return s[..end]
}

/// Wraps all of `src` into a list of display lines, each a view of a copy
/// of `src` in `a`, so the list holds nothing of the caller's. O(n · lines);
/// allocates the copy and the list in `a`.
export WrapAll(a: &Arena, src: str, width: int): OutOfMemory!List[str] from a {
    text := try a.Dup(src)
    var out := List[str].New(a)
    for s in Wrap(text, width) {
        try out.Push(text[s.start..s.end])
    }
    return out
}
