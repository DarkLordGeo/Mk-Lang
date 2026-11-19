// Why: Arena.Fmt's per-call body is synthesized by the compiler; the work
// that needs no generics lives here, written once.
// What: FmtArena and FmtArenaPad, the runners behind Arena.Fmt (layer 1:
// they allocate).
// How: a Text in a, its Writer, FmtRun or FmtPadRun, then the text's str
// (TextInto, by value). Any WriteError, including one a user Format returns,
// becomes OutOfMemory.

/// Formats pieces and args into a new str in a. Internal to std: every
/// Arena.Fmt call compiles to a call of this. Any WriteError becomes
/// OutOfMemory. O(output length); allocates the text.
export FmtArena(a: &Arena, pieces: []FmtPiece, args: []FmtArg): OutOfMemory!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    FmtRun(mut w, pieces, args) else {
        fail OutOfMemory
    }
    return TextInto(t) // the arena's bytes, from a; t.Str() would borrow t (E0603)
}

/// FmtArena for an Arena.Fmt call in which some hole has a width.
/// OutOfMemory as FmtArena. O(output length); allocates the text.
export FmtArenaPad(a: &Arena, pieces: []FmtPiece, args: []FmtArg): OutOfMemory!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    FmtPadRun(mut w, pieces, args) else {
        fail OutOfMemory
    }
    return TextInto(t)
}
