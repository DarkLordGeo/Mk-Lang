# std.text

`std.text` holds the Unicode algorithms: grapheme clusters, display width, normalization, case folding, word, sentence and line boundaries, bidirectional text, character properties and UTF-16. Reach for it when text is more than bytes. Use it to count what a reader sees as characters, line text up in a terminal, compare text that may be spelled two ways, wrap lines or lay out mixed left-to-right and right-to-left text. For splitting, finding and replacing, use `str`'s own methods (see [strings and text](core.md#strings-and-text)).

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.text
```

## Contents

- [The main pieces](#the-main-pieces)
- [Unicode data](#unicode-data)
- [Clusters and width](#clusters-and-width)
- [Boundaries and line breaks](#boundaries-and-line-breaks)
- [Normalization and case folding](#normalization-and-case-folding)
- [Bidirectional text](#bidirectional-text)
- [Properties](#properties)
- [UTF-16](#utf-16)
- [Writers with no allocator](#writers-with-no-allocator)
- [Memory and cost](#memory-and-cost)
- [Examples](#examples): [count clusters and columns](#count-clusters-and-columns), [compare text without case](#compare-text-without-case), [wrap lines](#wrap-lines), [lay out mixed directions](#lay-out-mixed-directions), [write with no allocator](#write-with-no-allocator)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Walk what a reader sees as characters | `Graphemes`, then `Clusters.Next` until `none` |
| Measure text in terminal columns | `Width` |
| Find words, sentences, paragraphs | `WordBounds`, `SentenceBounds`, `ParagraphBounds`, then `Bounds.Next` |
| Find where a line may wrap | `LineBreaks`, then `Breaks.Next` |
| One spelling for equal text | `Normalize` with a `Form` (`Nfc`, `Nfd`, `Nfkc`, `Nfkd`) |
| Compare without case | `FoldCase` |
| Order mixed-direction text for display | `BidiLevels`, then `Levels.Line`; `Mirror` for brackets |
| Ask about one character | `CategoryOf`, `ScriptOf`, `ScriptExtensions`, `Has` |
| Convert to and from UTF-16 | `ToUtf16`, `FromUtf16` |
| Format with no arena and no `Io` | `Capped`, `Sink` |
| Which Unicode version | `UnicodeVersion` |

## Unicode data

The tables of `std.text`, `regex`'s classes and the prelude's tables (`str`'s case mapping and `White_Space`) come from one version of the Unicode Character Database, which `text.UnicodeVersion()` returns: `"18.0.0"`. Every algorithm follows Unicode's default rules; none tailors them to a language or locale (there is no Turkish dotless-i folding, for example).

## Clusters and width

A grapheme cluster is what a reader sees as one character: a letter with its accents, a flag, a family emoji. `Graphemes(s)` returns a `Clusters` iterator over Unicode's extended grapheme clusters (UAX 29). Each cluster is a `str` view of `s`, and `""` has none. The iterator is named `Clusters` because a type and a function of one module cannot share a name (`E0206`).

`Width(s)` is the number of terminal columns `s` takes: the sum of its clusters' widths. A cluster is as wide as its first scalar:

| Width | Scalars |
|---|---|
| 0 | Controls, CR, LF, combining marks (Extend), the zero width joiner |
| 2 | East Asian Wide or Fullwidth, emoji shown as emoji (Emoji_Presentation), regional indicators |
| 1 | Everything else |

A pictograph followed by U+FE0F (emoji presentation) is 2 columns, and followed by U+FE0E (text presentation) is 1.

## Boundaries and line breaks

`WordBounds` and `SentenceBounds` follow UAX 29's default rules, and `ParagraphBounds` splits at paragraph separators (UAX 9's rule P1). Each returns a `Bounds` iterator of byte offsets in increasing order: 0, each boundary inside the text, then its length. The text between two offsets is one segment: for words, a word, a run of spaces or a punctuation mark. A paragraph separator is a line feed, a carriage return (CR LF counts as one), U+2029 or another scalar of Bidi_Class B, and each paragraph keeps its separator.

`LineBreaks` returns UAX 14's default line-break opportunities (rules LB2 to LB31) as `(at: int, mandatory: bool)`. A line may end at any opportunity and must end at a mandatory one:

- An opportunity is mandatory after a line break character: BK (form feed, U+2028 and others), LF, NL (NEL), or a CR not followed by LF. Every other one is allowed.
- There is none at offset 0.
- The last is the text's length, mandatory only when the text ends with a line break character.

## Normalization and case folding

`Normalize(a, s, form)` follows UAX 15. Text that Unicode treats as equal, such as U+00E9 and `e` followed by U+0301, has one spelling in each form:

| Form | Does |
|---|---|
| `Nfd` | Canonical decomposition |
| `Nfc` | Canonical decomposition, then composition |
| `Nfkd` | Compatibility decomposition, which also folds variants such as ligatures and full-width letters |
| `Nfkc` | Compatibility decomposition, then composition |

`FoldCase(a, s)` is Unicode's full default case folding, for comparing text without regard to case. One scalar may fold to several, so `FoldCase("Straße")` is `"strasse"`. `str.ToLower` maps one scalar to one and keeps `ß`. To compare text that may differ in both case and spelling, normalize and then fold, as the [example](#compare-text-without-case) does.

## Bidirectional text

`BidiLevels(a, s, dir)` resolves the embedding levels of one paragraph by the Unicode Bidirectional Algorithm (UAX 9, rules P2 to I2). An even level is left to right, an odd one right to left. `dir` is `Direction.Ltr`, `Direction.Rtl`, or `Direction.Auto`, which takes the direction from the paragraph's first strong character outside an isolate, and is left to right when there is none.

- Split a text into paragraphs with `ParagraphBounds` first. A paragraph separator inside `s` ends every embedding, override and isolate, as the end of a paragraph does. So each paragraph gets the levels it would get alone with the same direction. Under `Auto`, that is the direction of the first paragraph, for every paragraph of the call.
- `Levels.Base` is the paragraph level: 0 or 1.
- `Levels.At(i)` is the level of the scalar at byte `i`, before the line rules apply. A character the algorithm sets aside (an embedding, override or pop control, or one of Bidi_Class BN) has the level of the scalar before it.
- `Levels.Line(a, start, end)` applies the line rules L1 and L2 to the line from byte `start` to `end`, and returns its `Run`s in display order, left to right. Each run is a longest span of one level. Trailing whitespace and separators take the paragraph level.
- Drawing is yours: show the clusters of a run at an odd level in reverse order, and each mirrored scalar as its `Mirror` glyph, such as `)` for `(`.

## Properties

`CategoryOf(c)` is a scalar's General_Category, `ScriptOf(c)` its Script, `ScriptExtensions(c)` the scripts it is used with (never empty), and `Has(c, p)` tests a binary property. They read the same tables as the rest of std. The first two end in `Of` because a function cannot share its name with the enum it returns (`E0206`).

## UTF-16

`ToUtf16(a, s)` writes no byte order mark; a scalar above U+FFFF becomes a surrogate pair. `FromUtf16(a, units)` fails with `Utf8Error` on an unpaired surrogate, and finds it before it allocates anything.

## Writers with no allocator

`Capped` and `Sink` make a `Writer` with no arena and no `Io`, so a freestanding program can format with `Print` before its heap works, and inside its panic hook. Neither allocates, and their `Writer`s carry an `Io` that does nothing.

- A `Capped` (`Capped.New(buf)`) keeps the bytes that fit in `buf` and counts the rest. Its `Writer` never fails. `Bytes` is what was kept, `Dropped` how many bytes were not, saturating at the largest `int`, and `Reset` empties it.
- A `Sink[S]` is a stored callback: `Sink.New(write)` names a function that sends one run of bytes to an `S`, a device or any state. `Writer(s, buf)` makes a `Writer` that holds `s` exclusively and gathers writes in `buf`.
- A write that would overflow `buf` first fills it from its own bytes, and `write` gets `buf` full. Once nothing is gathered, the rest of a run that still does not fit goes to `write` whole, uncopied. So `"ab"`, a 10-byte run and `"cd"` through a 4-byte `buf` reach `write` as 4, 8 and, at `Flush`, 2 bytes.
- With an empty `buf`, every write that is not empty is one call of `write`, and an empty write makes none. `Flush` hands `write` what is gathered and does nothing more. A failure of `write` is the `Writer`'s `WriteError`.
- `Sink.New` runs at compile time, so a `const` can hold a `Sink`, and `(&C).Writer(s, buf)` makes a `Writer` over the const `C`.

## Memory and cost

Iterators (`Graphemes`, `WordBounds`, `SentenceBounds`, `ParagraphBounds`, `LineBreaks`) and the one-scalar queries allocate nothing, and what they return are views of the text or static data. A whole walk over a text is O(n). Functions that build a result (`Normalize`, `FoldCase`, `ToUtf16`, `FromUtf16`, `BidiLevels`, `Levels.Line`) take an `a: &Arena`, put the result there, and fail only with `OutOfMemory` (and `FromUtf16` with `Utf8Error`). Each function's doc states its cost and what it allocates.

## Examples

### Count clusters and columns

```mk
// verdict: ok
import std.text

export Main(sys: &System): WriteError!void {
    words := ["cafe\u{301}", "\u{1F1EF}\u{1F1F5}", "日本語", "\u{1F469}\u{200D}\u{1F4BB}"]
    for w in words[..] {
        var n := 0
        for _ in text.Graphemes(w) { // one cluster is what a reader sees as one character
            n += 1
        }
        try sys.Print("{w}  bytes: {bytes}  clusters: {n}  columns: {cols}\n", (w, bytes: w.Len(), n, cols: text.Width(w)))
    }
}
```

```text
$ mk run clusters.mk
café  bytes: 6  clusters: 4  columns: 4
🇯🇵  bytes: 8  clusters: 1  columns: 2
日本語  bytes: 9  clusters: 3  columns: 6
👩‍💻  bytes: 11  clusters: 1  columns: 2
```

- `"cafe\u{301}"` is five scalars but four clusters: the accent joins the `e`.
- A flag is two regional indicators and one cluster; the technologist is two emoji joined by U+200D, also one cluster.

### Compare text without case

```mk
// verdict: ok
import std.text

// Same reports whether a and b are the same text, ignoring case and
// the way accents are spelled.
Same(a: &Arena, x: str, y: str): OutOfMemory!bool {
    fx := try text.FoldCase(a, try text.Normalize(a, x, text.Form.Nfd))
    fy := try text.FoldCase(a, try text.Normalize(a, y, text.Form.Nfd))
    return fx == fy
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    composed := "Caf\u{E9}"      // é as one scalar
    decomposed := "cafe\u{301}" // e, then a combining acute accent
    try sys.Print("bytes equal: {b}\n", (b: composed == decomposed))
    try sys.Print("same text: {b}\n", (b: try Same(sys.arena, composed, decomposed)))
    folded := try text.FoldCase(sys.arena, "Straße")
    try sys.Print("FoldCase: {folded}, ToLower: {lower}\n", (folded, lower: try "Straße".ToLower(sys.arena)))
}
```

```text
$ mk run compare.mk
bytes equal: false
same text: true
FoldCase: strasse, ToLower: straße
```

### Wrap lines

```mk
// verdict: ok
import std.text

// Wrap prints s in lines of at most cols columns, breaking only where
// Unicode allows a line break.
Wrap(sys: &System, s: str, cols: int): WriteError!void {
    var start := 0 // where the current line starts
    var end := 0   // the last opportunity that still fits
    for br in text.LineBreaks(s) {
        if text.Width(s[start..br.at].Trim()) > cols && end > start {
            try sys.Print("{line}\n", (line: s[start..end].Trim()))
            start = end
        }
        end = br.at
        if br.mandatory { // a line break in the text ends the line here
            try sys.Print("{line}\n", (line: s[start..end].Trim()))
            start = end
        }
    }
    if start < s.Len() { // the last line, with no line break after it
        try sys.Print("{line}\n", (line: s[start..].Trim()))
    }
}

export Main(sys: &System): WriteError!void {
    try Wrap(sys, "The quick brown fox jumps over the lazy dog, twice.\nThen it rests.", 16)
}
```

```text
$ mk run wrap.mk
The quick brown
fox jumps over
the lazy dog,
twice.
Then it rests.
```

- `Width` measures columns, so the same code wraps text with wide characters correctly.
- The line break in the text is a mandatory opportunity, so `twice.` ends a line even though more would fit.

### Lay out mixed directions

```mk
// verdict: ok
import std.text

export Main(sys: &System): OutOfMemory|WriteError!void {
    s := "car \u{5DE}\u{5DB}\u{5D5}\u{5E0}\u{5D9}\u{5EA} (red)" // English with a Hebrew word
    levels := try text.BidiLevels(sys.arena, s, text.Direction.Auto)
    try sys.Print("paragraph level {b}\n", (b: levels.Base()))
    runs := try levels.Line(sys.arena, 0, s.Len()) // display order, left to right
    for r in runs {
        dir := if r.level % 2 == 1 { "rtl" } else { "ltr" }
        try sys.Print("{dir} [{t}]\n", (dir, t: s[r.start..r.end]))
    }
    m := text.Mirror('(') else { '?' }
    try sys.Print("mirror of ( is {m}\n", (m))
}
```

```text
$ mk run bidi.mk
paragraph level 0
ltr [car ]
rtl [מכונית]
ltr [ (red)]
mirror of ( is )
```

- The paragraph starts with a Latin letter, so `Auto` makes it left to right.
- The Hebrew run is at level 1. A renderer draws its clusters in reverse order.

### Write with no allocator

```mk
// verdict: ok
import std.text

// Counter is a device: it counts the calls and bytes it is sent.
struct Counter {
    calls: int
    bytes: int
}

Deliver(c: mut Counter, b: []u8): WriteError!void {
    c.calls += 1
    c.bytes += b.Len()
}

const Port = text.Sink[Counter].New(Deliver) // a Sink can be a const

export Main(sys: &System): WriteError!void {
    // A Capped keeps what fits in the buffer and counts the rest.
    var buf: [8]u8 = [0; 8]
    var c := text.Capped.New(mut buf[..])
    var w := c.Writer()
    try Print(mut w, "{n} items ready", (n: 42))
    kept := str.From(c.Bytes()) else { "?" }
    try sys.Print("kept [{kept}], dropped {d}\n", (kept, d: c.Dropped()))

    // A Sink's Writer gathers small writes in its buffer.
    var dev := Counter(calls: 0, bytes: 0)
    var small: [4]u8 = [0; 4]
    var sw := (&Port).Writer(mut dev, mut small[..])
    try sw.Str("ab")
    try sw.Str("0123456789")
    try sw.Str("cd")
    try sw.Flush()
    try sys.Print("{calls} calls, {bytes} bytes\n", (calls: dev.calls, bytes: dev.bytes))
}
```

```text
$ mk run capped.mk
kept [42 items], dropped 6
3 calls, 14 bytes
```

- `42 items ready` is 14 bytes. The `Capped` keeps the first 8 and counts the other 6.
- The Sink's `write` is called three times, with 4, 8 and 2 bytes.

## API

<!-- api:begin -->
Stability: Stable.

### `BidiLevels`

```
export BidiLevels(a: &Arena, s: str, dir: Direction): OutOfMemory!Levels from a, s
```

- Flows: The result borrows from a and s.
- Errors: OutOfMemory

The embedding levels of s as one paragraph, by the Unicode
Bidirectional Algorithm (UAX 9), in a. dir is the paragraph's
direction, or Auto to take it from the text.

Split a text into paragraphs with ParagraphBounds first. A paragraph
separator inside s ends every embedding, override and isolate, and the
isolating run sequence, so each paragraph of s gets the levels it would
get alone with the same direction; Auto reads only the text before the
first separator.

Fails only with OutOfMemory. O(n) for every input; allocates n bytes of
levels in a, and about 40 bytes a scalar of scratch in a sub-arena of
a, freed before it returns.

### `Bounds`

```
export view struct Bounds
```

An iterator over boundaries, from text.WordBounds,
text.SentenceBounds or text.ParagraphBounds: call Next until none.

### `Bounds.Next`

```
export Bounds.Next(mut self): ?int
```

The next boundary, a byte offset into the text; none after the last
one. O(n) over the whole walk for every input, each scalar decoded at
most three times (twice for SentenceBounds). One call is O(segment
length), except that SentenceBounds may read ahead after a full stop to
the next letter, paragraph separator or sentence terminator to decide
whether the sentence ends there. Allocates nothing.

### `Breaks`

```
export view struct Breaks
```

An iterator over line-break opportunities, from text.LineBreaks: call
Next until none.

### `Breaks.Next`

```
export Breaks.Next(mut self): ?(at: int, mandatory: bool)
```

The next opportunity: its byte offset, and whether the line must end
there; none after the last one. O(n) over the whole walk; one call is
O(length to the next opportunity). Allocates nothing.

### `Capped`

```
export view struct Capped
```

A fixed buffer to write into that keeps what fits and counts what does
not. Make one over your buffer with `New`; `Writer` gives the Writer
that fills it, whose writes never fail; `Bytes` is what was kept,
`Dropped` how many bytes were not (saturating at the largest int), and
`Reset` empties it for the next line. Needs no Arena and no Io, and
allocates nothing.

### `Capped.Bytes`

```
export Capped.Bytes(&self): []u8 from self
```

- Flows: The result borrows from self.

Returns the bytes kept, in the order written. O(1).

### `Capped.Dropped`

```
export Capped.Dropped(&self): int
```

Returns the number of bytes written past the buffer's end and not kept,
saturating at the largest int. O(1).

### `Capped.New`

```
export Capped.New(buf: mut []u8): Capped from buf
```

- Flows: The result borrows from buf.

Returns an empty target over buf. O(1); allocates nothing.

### `Capped.Reset`

```
export Capped.Reset(mut self)
```

Empties the target for the next record. O(1).

### `Capped.Writer`

```
export Capped.Writer(mut self): Writer from self
```

- Flows: The result borrows from self.

Returns a Writer with no inline buffer that appends to self: each write
keeps the bytes that fit, counts the rest, and never fails. It needs no
Io: it carries one that does nothing, so formatting into it runs at
compile time. O(1); allocates nothing; each write is O(its bytes) and
one table call.

### `Category`

```
export enum Category
```

A scalar's General_Category, by its long name in the UCD (Lu is
UppercaseLetter). The set of values is closed by Unicode's stability
policy.

Variants:

- `UppercaseLetter`
- `LowercaseLetter`
- `TitlecaseLetter`
- `ModifierLetter`
- `OtherLetter`
- `NonspacingMark`
- `SpacingMark`
- `EnclosingMark`
- `DecimalNumber`
- `LetterNumber`
- `OtherNumber`
- `ConnectorPunctuation`
- `DashPunctuation`
- `OpenPunctuation`
- `ClosePunctuation`
- `InitialPunctuation`
- `FinalPunctuation`
- `OtherPunctuation`
- `MathSymbol`
- `CurrencySymbol`
- `ModifierSymbol`
- `OtherSymbol`
- `SpaceSeparator`
- `LineSeparator`
- `ParagraphSeparator`
- `Control`
- `Format`
- `Surrogate`
- `PrivateUse`
- `Unassigned`

### `CategoryOf`

```
export CategoryOf(c: char): Category
```

The General_Category of c, such as UppercaseLetter or DecimalNumber;
Unassigned for a scalar Unicode does not assign. O(1); allocates
nothing.

### `Clusters`

```
export view struct Clusters
```

An iterator over grapheme clusters, from text.Graphemes: call Next
until none.

### `Clusters.Next`

```
export Clusters.Next(mut self): ?str from self.text
```

- Flows: The result borrows what self.text refers to; self is free after the call.

The next cluster, a view of the text; none after the last one. An ASCII
byte other than CR, followed by another ASCII byte or by the end, is a
cluster by itself. O(cluster length); allocates nothing.

### `Direction`

```
export open enum Direction
```

The direction of a paragraph, for text.BidiLevels. Auto takes it from
the paragraph's first strong character outside an isolate, and is left
to right when there is none. Ltr and Rtl set it whatever the text holds.

Variants:

- `Auto`: From the paragraph's first strong character outside an isolate; left to
  right when there is none.
- `Ltr`: Left to right, whatever the text holds.
- `Rtl`: Right to left, whatever the text holds.

### `FoldCase`

```
export FoldCase(a: &Arena, s: str): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

s with Unicode's full default case folding, in a: the form to compare
text without regard to case. One scalar may fold to several, so
"Stra\u{DF}e" folds to "strasse" and U+0130 to "i\u{307}"; that is the
difference from str.ToLower, which maps one scalar to one. Turkic
dotless-i folding is not applied. Fails only with OutOfMemory. O(n), two
passes; allocates the result, exactly its length, in a (one copy of s
when nothing folds).

### `Form`

```
export enum Form
```

A Unicode normalization form (UAX 15). Nfd decomposes canonically and
Nfc then composes again; Nfkd and Nfkc do the same with compatibility
decomposition, which also folds variants such as ligatures and
full-width letters.

Variants:

- `Nfc`
- `Nfd`
- `Nfkc`
- `Nfkd`

### `FromUtf16`

```
export FromUtf16(a: &Arena, units: []u16): Utf8Error|OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: Utf8Error, OutOfMemory

The text the UTF-16 code units spell, in a. Utf8Error on an unpaired
surrogate: a high one (D800 to DBFF) not followed by a low one (DC00 to
DFFF), or a low one not after a high one. That check runs before
anything is allocated. O(n); then one allocation in a of exactly the
bytes needed.

### `Graphemes`

```
export Graphemes(s: str): Clusters
```

- Flows: The result borrows from s.

The grapheme clusters of s in order: what a reader sees as one
character, such as "e" with a combining accent, a flag or a family
emoji. These are UAX 29's extended grapheme clusters. "" yields none.
O(1); allocates nothing.

### `Has`

```
export Has(c: char, p: Property): bool
```

Whether c has the binary property p, as the Unicode Character Database
states it. Any, ASCII and Assigned follow Unicode regular expressions
(UTS 18). O(1); allocates nothing.

### `Levels`

```
export view struct Levels
```

The resolved embedding levels of one paragraph, from text.BidiLevels.

### `Levels.At`

```
export Levels.At(&self, i: int): u8
```

The resolved level of the scalar at byte offset i, which may be any
byte of the scalar. This is the level before line rules apply: Line
resets trailing whitespace and separators to the paragraph level. A
character the algorithm sets aside (an embedding, override or pop
control, or one of Bidi_Class BN) has the level of the scalar before
it, or the paragraph level at the start.
Panics unless 0 <= i < the text's length. O(1); allocates nothing.

### `Levels.Base`

```
export Levels.Base(&self): u8
```

The paragraph embedding level: 0 left to right, 1 right to left.

### `Levels.Line`

```
export Levels.Line(&self, a: &Arena, start: int, end: int): OutOfMemory![]Run from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

The runs of the line made of the paragraph's bytes start to end, in
display order from left to right, in a. Each run is a longest span of
one level, and the runs cover the line.

Segment and paragraph separators, and whitespace and isolate controls
before one or at the end of the line, take the paragraph level. Then
runs are reordered: for each odd level from the highest down, every
sequence of runs at or above it is reversed.

Panics unless start and end are scalar boundaries with start <= end.
Fails only with OutOfMemory. O(line length x levels); allocates the
runs in a, and the line's bytes of scratch in a sub-arena of a, freed
before it returns.

### `LineBreaks`

```
export LineBreaks(s: str): Breaks
```

- Flows: The result borrows from s.

The places where a line of s may be wrapped, by Unicode's default
line-breaking rules (UAX 14), in increasing byte order. A line may end
at any opportunity and must end at a mandatory one: the one after a
line break character (a CR not followed by LF, LF, NEL, a form feed,
U+2028 and the rest of Line_Break classes BK, CR, LF and NL). Every
other opportunity is allowed. There is none at 0. The last is s.Len(),
mandatory only when s ends with a line break character. "" yields none.
O(1); allocates nothing.

### `Mirror`

```
export Mirror(c: char): ?char
```

The mirrored form of c (its Bidi_Mirroring_Glyph): the scalar a
renderer shows in its place at an odd level, such as ')' for '('. None
when c has none. O(log n) in the number of mirrored scalars; allocates
nothing.

### `Normalize`

```
export Normalize(a: &Arena, s: str, form: Form): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

s in the normalization form, in a: one spelling for text that Unicode
treats as equal, such as U+00E9 and "e" followed by U+0301. Use it
before comparing, hashing or storing text. Text already in the form is
one copy. Fails only with OutOfMemory. O(n) for every input; allocates
the result and, when a long run of combining marks decomposes to more
than 32 scalars, one scratch buffer in a.

### `ParagraphBounds`

```
export ParagraphBounds(s: str): Bounds
```

- Flows: The result borrows from s.

The paragraph boundaries of s, as byte offsets in increasing order: 0,
the offset after each paragraph separator inside s, then s.Len(). ""
yields none. A paragraph separator is a line feed, a carriage return
(CR LF counts as one), U+2029 or another scalar of Bidi_Class B. Each
paragraph keeps its separator. O(1); allocates nothing.

### `Property`

```
export open enum Property
```

A binary property Has answers: ECMA-262's binary Unicode properties,
by their UCD names (ID_Start is IdStart). A later version may add
properties.

Variants:

- `Ascii`
- `AsciiHexDigit`
- `Alphabetic`
- `Any`
- `Assigned`
- `BidiControl`
- `BidiMirrored`
- `CaseIgnorable`
- `Cased`
- `ChangesWhenCasefolded`
- `ChangesWhenCasemapped`
- `ChangesWhenLowercased`
- `ChangesWhenNfkcCasefolded`
- `ChangesWhenTitlecased`
- `ChangesWhenUppercased`
- `Dash`
- `DefaultIgnorableCodePoint`
- `Deprecated`
- `Diacritic`
- `Emoji`
- `EmojiComponent`
- `EmojiModifier`
- `EmojiModifierBase`
- `EmojiPresentation`
- `ExtendedPictographic`
- `Extender`
- `GraphemeBase`
- `GraphemeExtend`
- `HexDigit`
- `IdsBinaryOperator`
- `IdsTrinaryOperator`
- `IdContinue`
- `IdStart`
- `Ideographic`
- `JoinControl`
- `LogicalOrderException`
- `Lowercase`
- `Math`
- `NoncharacterCodePoint`
- `PatternSyntax`
- `PatternWhiteSpace`
- `QuotationMark`
- `Radical`
- `RegionalIndicator`
- `SentenceTerminal`
- `SoftDotted`
- `TerminalPunctuation`
- `UnifiedIdeograph`
- `Uppercase`
- `VariationSelector`
- `WhiteSpace`
- `XidContinue`
- `XidStart`

### `Run`

```
export struct Run
```

A run of a line, from Levels.Line: the bytes start to end of the
paragraph's text, all at one embedding level. An odd level is right to
left. Reversing the clusters of such a run and drawing each mirrored
scalar as its Mirror glyph is the renderer's job.

Fields:

- `start: int`: The byte offset of the run's start in the paragraph's text.
- `end: int`: The byte offset where the run ends, exclusive.
- `level: u8`: The embedding level; an odd level is right to left.

### `Script`

```
export open enum Script
```

A Script value, by its long name in the UCD (Common, Inherited and
Unknown among them; Old_Italic is OldItalic). A later Unicode adds
scripts.

Variants:

- `Adlam`
- `Ahom`
- `AnatolianHieroglyphs`
- `Arabic`
- `Armenian`
- `Avestan`
- `Balinese`
- `Bamum`
- `BassaVah`
- `Batak`
- `Bengali`
- `BeriaErfe`
- `Bhaiksuki`
- `Bopomofo`
- `Brahmi`
- `Braille`
- `Buginese`
- `Buhid`
- `CanadianAboriginal`
- `Carian`
- `CaucasianAlbanian`
- `Chakma`
- `Cham`
- `Cherokee`
- `Chorasmian`
- `Common`
- `Coptic`
- `Cuneiform`
- `Cypriot`
- `CyproMinoan`
- `Cyrillic`
- `Deseret`
- `Devanagari`
- `DivesAkuru`
- `Dogra`
- `Duployan`
- `EgyptianHieroglyphs`
- `Elbasan`
- `Elymaic`
- `Ethiopic`
- `Garay`
- `Georgian`
- `Glagolitic`
- `Gothic`
- `Grantha`
- `Greek`
- `Gujarati`
- `GunjalaGondi`
- `Gurmukhi`
- `GurungKhema`
- `Han`
- `Hangul`
- `HanifiRohingya`
- `Hanunoo`
- `Hatran`
- `Hebrew`
- `Hiragana`
- `ImperialAramaic`
- `Inherited`
- `InscriptionalPahlavi`
- `InscriptionalParthian`
- `Javanese`
- `Jurchen`
- `Kaithi`
- `Kannada`
- `Katakana`
- `KatakanaOrHiragana`
- `Kawi`
- `KayahLi`
- `Kharoshthi`
- `KhitanSmallScript`
- `Khmer`
- `Khojki`
- `Khudawadi`
- `KiratRai`
- `Lao`
- `Latin`
- `Lepcha`
- `Limbu`
- `LinearA`
- `LinearB`
- `Lisu`
- `Lycian`
- `Lydian`
- `Mahajani`
- `Makasar`
- `Malayalam`
- `Mandaic`
- `Manichaean`
- `Marchen`
- `MasaramGondi`
- `Medefaidrin`
- `MeeteiMayek`
- `MendeKikakui`
- `MeroiticCursive`
- `MeroiticHieroglyphs`
- `Miao`
- `Modi`
- `Mongolian`
- `Mro`
- `Multani`
- `Myanmar`
- `Nabataean`
- `NagMundari`
- `Nandinagari`
- `NewTaiLue`
- `Newa`
- `Nko`
- `Nushu`
- `NyiakengPuachueHmong`
- `Ogham`
- `OlChiki`
- `OlOnal`
- `OldHungarian`
- `OldItalic`
- `OldNorthArabian`
- `OldPermic`
- `OldPersian`
- `OldSogdian`
- `OldSouthArabian`
- `OldTurkic`
- `OldUyghur`
- `Oriya`
- `Osage`
- `Osmanya`
- `PahawhHmong`
- `Palmyrene`
- `PauCinHau`
- `PhagsPa`
- `Phoenician`
- `ProtoCuneiform`
- `PsalterPahlavi`
- `Rejang`
- `Runic`
- `Samaritan`
- `Saurashtra`
- `Seal`
- `Sharada`
- `Shavian`
- `Siddham`
- `Sidetic`
- `SignWriting`
- `Sinhala`
- `Sogdian`
- `SoraSompeng`
- `Soyombo`
- `Sundanese`
- `Sunuwar`
- `SylotiNagri`
- `Syriac`
- `Tagalog`
- `Tagbanwa`
- `TaiLe`
- `TaiTham`
- `TaiViet`
- `TaiYo`
- `Takri`
- `Tamil`
- `Tangsa`
- `Tangut`
- `Telugu`
- `Thaana`
- `Thai`
- `Tibetan`
- `Tifinagh`
- `Tirhuta`
- `Todhri`
- `TolongSiki`
- `Toto`
- `TuluTigalari`
- `Ugaritic`
- `Unknown`
- `Vai`
- `Vithkuqi`
- `Wancho`
- `WarangCiti`
- `Yezidi`
- `Yi`
- `ZanabazarSquare`

### `ScriptExtensions`

```
export ScriptExtensions(c: char): []Script
```

The scripts c is used with (its Script_Extensions, UAX 24), ordered by
the scripts' codes, or ScriptOf(c) alone when Unicode lists none for c.
Never empty. The slice is static data. O(1); allocates nothing.

### `ScriptOf`

```
export ScriptOf(c: char): Script
```

The Script of c, such as Latin or Greek; Common for punctuation and
symbols that many scripts share, and Unknown for a scalar no script
lists. O(1); allocates nothing.

### `SentenceBounds`

```
export SentenceBounds(s: str): Bounds
```

- Flows: The result borrows from s.

The sentence boundaries of s, by Unicode's default sentence rules
(UAX 29), as byte offsets in increasing order: 0, each boundary inside
s, then s.Len(). "" yields none. O(1); allocates nothing.

### `Sink`

```
export struct Sink[S]
```

Makes Writers over your own device or state of type S. `New(write)`
names the function that sends one run of bytes to it, and
`Writer(s, buf)` makes a Writer that holds s exclusively and buffers in
buf. Needs no Arena and no Io, and allocates nothing. New runs at
compile time and the result is plain, so a const can hold it.

### `Sink.New`

```
export Sink[S].New(write: fn(mut S, []u8): WriteError!void): Sink[S]
```

Returns a Sink over write. O(1); allocates nothing; runs at compile
time, so a const holds it, and (&C).Writer(s, buf) makes a Writer over
the const C.

### `Sink.Writer`

```
export Sink[S].Writer(&self, s: mut S, buf: mut []u8): Writer from self, s, buf
```

- Flows: The result borrows from self, s and buf.

Returns a Writer over s, which it holds exclusively, that writes through
self's write, with buf as its inline buffer. A write that would overflow
buf fills it up and hands it to write full; once nothing is buffered,
the rest of a run that still exceeds buf goes to write whole, uncopied,
as Writer.Bytes says. So "ab", a 10-byte run and "cd" through a 4-byte
buf reach write as 4, 8 and, at Flush, 2 bytes. With an empty buf every
write that is not empty is one call of write, and an empty write makes
none. Flush hands write what is buffered and does nothing more. A
failure of write is the Writer's WriteError. O(1); allocates nothing.

### `ToUtf16`

```
export ToUtf16(a: &Arena, s: str): OutOfMemory![]u16 from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

s as UTF-16 code units, with no byte order mark, in a. A scalar above
U+FFFF takes two units (a surrogate pair), any other one. Fails only
with OutOfMemory. O(n); one allocation in a of exactly the units
needed.

### `UnicodeVersion`

```
export UnicodeVersion(): str
```

The version of Unicode that std.text's data comes from, such as
"18.0.0". Every Unicode table of std, str's case mapping and regex's
classes included, comes from this one version. O(1); allocates
nothing.

### `Width`

```
export Width(s: str): int
```

The number of terminal columns s takes: the sum of its grapheme
clusters' widths. A cluster is as wide as its first scalar: 0 for a
control, CR, LF, a combining mark (Extend) and ZWJ; 2 for a wide or
full-width scalar (East_Asian_Width W or F), an emoji shown as emoji
(Emoji_Presentation) and a regional indicator; 1 for the rest. A
cluster led by a pictograph (Extended_Pictographic) is 2 when U+FE0F
follows it and 1 when U+FE0E does. O(n); allocates nothing.

### `WordBounds`

```
export WordBounds(s: str): Bounds
```

- Flows: The result borrows from s.

The word boundaries of s, by Unicode's default word rules (UAX 29), as
byte offsets in increasing order: 0, each boundary inside s, then
s.Len(). "" yields none. The text between two consecutive offsets is a
word, a run of spaces, a punctuation mark or another segment. O(1);
allocates nothing.

<!-- api:end -->
