// Why: Compile picks a strategy per pattern, and each must match the same.
// What: tests of Compile's literal, byte-class, folded and word matchers,
// and of a pattern error's offset.
// How: each test compiles a pattern and searches a few lines.

test "a pattern with no regex syntax is searched as a literal" (sys: &System) {
    patterns := ["needle"]
    spec := Spec(patterns: patterns[..], fixed: false, fold: false, word: false)
    m := try Compile(sys.arena, &spec)
    Assert(m.mode == Mode.Literal)
    Assert(m.IsMatch("a needle here"))
    Assert(!m.IsMatch("a needl here"))
}

test "a bracket class of plain bytes is searched as a byte class" (sys: &System) {
    patterns := ["[;#]"]
    spec := Spec(patterns: patterns[..], fixed: false, fold: false, word: false)
    m := try Compile(sys.arena, &spec)
    Assert(m.mode == Mode.ByteClass)
    Assert(m.IsMatch("a; b"))
    Assert(!m.IsMatch("a, b"))
}

test "a folded literal matches either case past sixteen bytes" (sys: &System) {
    patterns := ["needle"]
    spec := Spec(patterns: patterns[..], fixed: true, fold: true, word: false)
    m := try Compile(sys.arena, &spec)
    Assert(m.IsMatch("0123456789abcdefghij NEEDLE"))
    Assert(m.IsMatch("NeEdLe"))
}

test "a word literal skips matches inside longer words" (sys: &System) {
    patterns := ["needle"]
    spec := Spec(patterns: patterns[..], fixed: false, fold: false, word: true)
    m := try Compile(sys.arena, &spec)
    Assert(!m.IsMatch("needles"))
    found := m.Find("needles and a needle", 0).Must()
    Assert(found.lo == 14)
}

test "an unclosed group is reported at the user's offset" (sys: &System) {
    patterns := ["ok", "x(y"]
    spec := Spec(patterns: patterns[..], fixed: false, fold: false, word: false)
    _ = Compile(sys.arena, &spec) else e {
        match e {
            BadPattern(index, offset) {
                Assert(index == 1)
                Assert(offset == 1)
            }
            _ {
                Assert(false)
            }
        }
        return
    }
    Assert(false)
}
