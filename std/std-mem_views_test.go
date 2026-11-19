// Why: whether a std type is a view or a carrier is public; a private edit
// must not change it, and the expected values come from the reference.
// What: the view and carrier columns of the std kind pins, as mk check
// probes; std-mem_kinds_test.go pins kind, Send and Share.
// How: two signature rules stand in for check's type facts; see
// TestStdViewsPinned.

package std

import (
	"fmt"
	"strings"
	"testing"
)

type viewRow struct {
	ty      string
	view    bool
	carrier bool
	why     string
}

var viewRows = []viewRow{
	{"List[int]", true, true, "view struct List; ref §3"},
	{"Map[int, int]", true, true, "std declares it so"},
	{"Set[int]", true, true, "std declares it so"},
	{"Text", true, true, "std declares it so"},
	{"Arena", true, true, "linear view struct Arena"},
	{"HeapList[int]", true, true, "std declares it so"},
	{"HeapMap[int, int]", true, true, "std declares it so"},
	{"TextMap[int]", true, true, "std declares it so"},
	{"HeapText", true, true, "std declares it so"},
	{"Box[int]", true, true, "std declares it so"},
	{"sync.Shared[int]", true, true, "std declares it so"},
	{"Guard[int]", true, true, "linear view struct Guard"},
	{"Chan[int]", true, true, "view struct Chan"},
	{"Reader", true, true, "std declares it so"},
	{"Writer", true, true, "std declares it so"},
	{"Split", true, true, "ref §3: so are Split, Lines, Words, Scalars, MapIter, TextMapIter"},
	{"Lines", true, true, "ref §3"},
	{"Words", true, true, "ref §3"},
	{"Scalars", true, true, "ref §3"},
	{"MapIter[int, int]", true, true, "ref §3"},
	{"TextMapIter[int]", true, true, "ref §3"},
	{"Cell[int]", false, false, "ref §3: Cell stores T inline"},
	{"Mutex[int]", false, false, "ref §3: Mutex stores T inline; not a view"},
	{"fs.File", false, false, "ref §3: File, Stream, Listener hold no references"},
	{"net.Stream", false, false, "ref §3"},
	{"net.Listener", false, false, "ref §3"},
	{"Io", false, false, "ref §3: Io is a compiler-known handle and holds no loans"},
	{"Alloc", false, true, "ref §3: a compiler-known handle, not a view; its table is &AllocTable, a reference, so it holds a loan (§3 states no carrier bit)"},
	{"&Heap", false, true, "ref §3: Heap is a compiler-known handle; a reference holds a loan"},
	{"atomic.AtomicInt", false, false, "unique, not a view"},
	{"atomic.AtomicBool", false, false, "std declares it so"},
	{"Hasher", false, false, "plain"},
	{"time.Duration", false, false, "plain"},
	{"time.Instant", false, false, "plain"},
	{"TargetInfo", false, false, "plain"},
	{"fs.Error", false, false, "payload-free but Os(code: i64)"},
	{"proc.Signal", false, false, "plain"},
}

// viewProgram returns the probe program and, per row, the lines of its V
// and C declarations.
func viewProgram(rows []viewRow) (string, [][2]int) {
	var b strings.Builder
	b.WriteString(kindImports)
	b.WriteString("struct Q {\n    x: int\n}\n\n")
	lines := make([][2]int, len(rows))
	for i, r := range rows {
		lines[i][0] = strings.Count(b.String(), "\n") + 1
		fmt.Fprintf(&b, "V%d(a: &Q, b: &Q): %s {\n    Panic(\"probe\")\n}\n\n", i, r.ty)
		lines[i][1] = strings.Count(b.String(), "\n") + 1
		fmt.Fprintf(&b, "C%d(a: %s, b: %s): List[int] {\n    Panic(\"probe\")\n}\n\n", i, r.ty, r.ty)
	}
	b.WriteString("export Main(sys: &System) {\n}\n")
	return b.String(), lines
}

// TestStdViewsPinned: every row of viewRows is a view and a carrier
// exactly as the reference states, as check sees it from a user module.
//
// Package std cannot reach check's type facts, so two signature rules of
// the reference stand in for them, with no body involved:
//
//   - view: `V(a: &Q, b: &Q): T`, with Q a struct of the probe's own that no
//     std type mentions. A view result "could come from any parameter that
//     can hold loans", so E0601 at V says T is a view; any other result
//     could come only from a parameter whose type holds what it refers to,
//     and Q holds none of it.
//   - carrier: `C(a: T, b: T): List[int]`. List is a view result, so
//     E0601 at C says a by-value T can hold loans (a carrier); "a by-value
//     non-carrier holds none".
//
// The expected values are written from the reference ("every type in this
// table with memory behind a private pointer is a view", with its listed
// exceptions; "view struct declares a raw-pointer type a carrier"), never
// from the code.
func TestStdViewsPinned(t *testing.T) {
	t.Parallel()
	src, lines := viewProgram(viewRows)
	out := checkUncapped(t, src)
	got := kindCodes(t, out, "E0601")
	for i, r := range viewRows {
		view := hasCode(got[lines[i][0]], "E0601")
		carrier := hasCode(got[lines[i][1]], "E0601")
		if view != r.view || carrier != r.carrier {
			t.Errorf("%s: view %v, carrier %v; want %v, %v (%s)", r.ty, view, carrier, r.view, r.carrier, r.why)
		}
	}
}

// TestViewProbesSeeKnownCases: the two probes on types of the probe's
// own, whose answers the reference states outright: a `view struct` over a
// raw pointer is a view and a carrier; a struct of an int is neither; a
// struct with a `str` field is a carrier and not a view.
func TestViewProbesSeeKnownCases(t *testing.T) {
	t.Parallel()
	src := `struct Q {
    x: int
}

view struct P {
    p: *u8
}

struct N {
    x: int
}

struct S {
    s: str
}

V0(a: &Q, b: &Q): P {
    Panic("probe")
}

C0(a: P, b: P): List[int] {
    Panic("probe")
}

V1(a: &Q, b: &Q): N {
    Panic("probe")
}

C1(a: N, b: N): List[int] {
    Panic("probe")
}

V2(a: &Q, b: &Q): S {
    Panic("probe")
}

C2(a: S, b: S): List[int] {
    Panic("probe")
}

export Main(sys: &System) {
}
`
	out := checkUncapped(t, src)
	got := kindCodes(t, out, "E0601")
	for _, c := range []struct {
		line int
		want bool
		what string
	}{{17, true, "view P"}, {21, true, "carrier P"}, {25, false, "view N"}, {29, false, "carrier N"}, {33, false, "view S"}, {37, true, "carrier S"}} {
		if hasCode(got[c.line], "E0601") != c.want {
			t.Errorf("%s: E0601 at main.mk:%d is %v, want %v\n%s", c.what, c.line, !c.want, c.want, out)
		}
	}
}
