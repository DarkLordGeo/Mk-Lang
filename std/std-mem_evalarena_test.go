// Why: EvalArena() and Gen.Arena() give compile-time code an ordinary Arena,
// and its data must survive into the program as static data.
// What: tests of MakeEvalArena, Arena.Fmt and the const user form.
// How: MakeEvalArena runs the Arena code over EvalPageAlloc and EvalPageFree,
// which the evaluator replaces by key; reaching their native body fails.

package std

import "testing"

// TestEvalArenaAtCompileTime: at compile time, an arena from
// MakeEvalArena gives fresh Put memory, zero-filled Slices (a large one in a
// chunk of its own), Dup, a Sub that frees through the page source, a Text,
// an Arena.Fmt with literal pieces on both sides, a List grown past several
// chunks, and a Box through its Alloc(); each check that fails adds one.
func TestEvalArenaAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "evalarena", `
EvalArenaProbe(): int {
    a := MakeEvalArena()
    var bad := 0
    p := a.Put(41).Must()
    q := a.Put(42).Must()
    *p += 1
    if *p != 42 || *q != 42 {
        bad += 1
    }
    s := a.Slice[int](1000).Must()
    for x in s {
        if x != 0 {
            bad += 1
        }
    }
    s[999] = 7
    big := a.Slice[u8](10_000).Must()
    big[9_999] = 1
    if big[0] != 0 || s[999] != 7 || *q != 42 {
        bad += 1
    }
    d := a.Dup("h\u{e9}llo").Must()
    if d != "h\u{e9}llo" || d.Len() != 6 {
        bad += 1
    }
    var sub := a.Sub().Must()
    _ = sub.Slice[u8](5_000).Must()
    sub.Free()
    var t := Text.New(a)
    t.Push("ab").Must()
    t.Push("cd").Must()
    if t.Str() != "abcd" {
        bad += 1
    }
    f := a.Fmt("a{x}b{y}c", (x: 5, y: "q")).Must()
    if f != "a5bqc" {
        bad += 1
    }
    var xs := List[int].New(a)
    for i in 0..5_000 {
        xs.Push(i).Must()
    }
    if xs.Len() != 5_000 || xs[4_999] != 4_999 {
        bad += 1
    }
    b := Box[int].New(a.Alloc(), 3).Must()
    if b.Take(a.Alloc()) != 3 {
        bad += 1
    }
    return bad
}

const EvalArenaChecks = [0; 1][EvalArenaProbe()]
`)
}

// TestEvalArenaFmtEmptyPiece: Arena.Fmt at compile time with a format
// string whose literal pieces are empty ("{x}", "{x}{y}"). An empty piece
// once held the dangling data pointer 1, which the evaluator refused
// (E0901 pointer-bytes in Writer.Bytes); a refusal fails the test.
func TestEvalArenaFmtEmptyPiece(t *testing.T) {
	t.Parallel()
	expectClean(t, "evalfmt", `
EvalFmtProbe(): int {
    a := MakeEvalArena()
    var bad := 0
    if a.Fmt("{x}", (x: 5)).Must() != "5" {
        bad += 1
    }
    if a.Fmt("{x}{y}", (x: 5, y: "q")).Must() != "5q" {
        bad += 1
    }
    return bad
}

const EvalFmtChecks = [0; 1][EvalFmtProbe()]
`)
}

// evalArenaConstProgram is the reference's user form:
// `const T = Build(EvalArena())` with `Build(a: &Arena)`; the arena's
// slices, lists and strs become static data. Greet formats with empty
// literal pieces ("{x}{y}") at compile time.
const evalArenaConstProgram = `Build(a: &Arena): int {
    s := a.Slice[int](1000).Must()
    for i in 0..s.Len() {
        s[i] = i
    }
    var xs := List[int].New(a)
    for x in s {
        xs.Push(x * 2).Must()
    }
    var sum := 0
    for x in xs {
        sum += x
    }
    return sum
}

Greet(a: &Arena): str from a {
    t := a.Fmt("{x}{y}", (x: 4, y: "2")).Must()
    return a.Dup(t).Must()
}

Table(a: &Arena): []int from a {
    s := a.Slice[int](5).Must()
    for i in 0..5 {
        s[i] = i * i
    }
    return s[..]
}

const Sum = Build(EvalArena())
const Name = Greet(EvalArena())
const Squares = Table(EvalArena())

export Main(sys: &System) {
    var out := sys.Stdout()
    Print(mut out, "{s} {n} {a} {b}\n", (s: Sum, n: Name, a: Squares.Len(), b: Squares[4])) else {
        return
    }
}
`

// TestEvalArenaConstForms: the user form of EvalArena() builds, and the program prints the values computed at
// compile time, at dev and at release.
func TestEvalArenaConstForms(t *testing.T) {
	t.Parallel()
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildNative(t, nil, evalArenaConstProgram, flags...), "")
		wantRun(t, r, 0, "999000 42 5 16\n")
	}
}
