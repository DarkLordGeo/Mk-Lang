// Why: a private edit in std must not change a public type's kind, Send or
// Share; the expected values come from the reference's table, never the code.
// What: kindRows, TestStdKindsPinned, the private-field and Scope tests.
// How: mk check probes, since package std cannot reach check's kinds (see
// TestStdKindsPinned for the two probe programs).

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// kindRow is one pinned type: its spelling in a user module (with the
// imports it needs) and its kind, Send and Share.
type kindRow struct {
	ty    string
	kind  string // "plain", "unique" or "linear"
	send  bool
	share bool
	why   string // the design line the row is transcribed from
}

// kindRows is the reference's library kind table, row by row, with one
// instance per "if contents are" condition on each side, plus std's plain
// types.
var kindRows = []kindRow{
	{"List[int]", "unique", false, false, "ref §3: List Map Set Text unique, no, no"},
	{"Map[int, int]", "unique", false, false, "ref §3"},
	{"Set[int]", "unique", false, false, "ref §3"},
	{"Text", "unique", false, false, "ref §3"},
	{"Arena", "linear", false, false, "ref §3: Arena linear, no, no"},
	{"&Heap", "plain", true, true, "ref §3: Heap reached by &Heap, shared yes"},
	{"HeapList[int]", "linear", true, true, "ref §3: heap containers linear, if contents are"},
	{"HeapList[List[int]]", "linear", false, false, "ref §3: if contents are"},
	{"HeapMap[int, int]", "linear", true, true, "ref §3"},
	{"HeapMap[int, Cell[int]]", "linear", true, false, "ref §3: Cell is Send, not Share"},
	{"TextMap[int]", "linear", true, true, "ref §3"},
	{"TextMap[List[int]]", "linear", false, false, "ref §3"},
	{"HeapText", "linear", true, true, "plan 3.2: unsafe impl Send, Share"},
	{"Box[int]", "linear", true, true, "ref §3"},
	{"Box[Cell[int]]", "linear", true, false, "ref §3"},
	{"Box[List[int]]", "linear", false, false, "ref §3"},
	{"sync.Shared[int]", "linear", true, true, "ref §3; plan 3.2: Send and Share if T: Send + Share"},
	{"sync.Shared[Cell[int]]", "linear", false, false, "plan 3.2: needs T: Send + Share for either"},
	{"fs.File", "linear", true, false, "ref §3: File Stream linear, yes, no"},
	{"net.Stream", "linear", true, false, "ref §3"},
	{"net.Listener", "linear", true, true, "ref §3: Listener linear, yes, yes"},
	{"Guard[int]", "linear", false, false, "ref §3: Guard linear, no, no"},
	{"Mutex[int]", "unique", true, true, "ref §3: Mutex Chan unique or linear, if T: Send"},
	{"Mutex[Cell[int]]", "unique", true, true, "ref §3: Share needs only T: Send"},
	{"Mutex[List[int]]", "unique", false, false, "ref §3"},
	{"Mutex[Box[int]]", "linear", true, true, "ref §3: linear if T is"},
	{"Chan[int]", "unique", true, true, "ref §3"},
	{"Chan[List[int]]", "unique", false, false, "ref §3"},
	{"Chan[Box[int]]", "linear", true, true, "ref §3"},
	{"Cell[int]", "unique", true, false, "ref §3: Cell unique, if T is, no"},
	{"Cell[time.Duration]", "unique", true, false, "ref §3"},
	{"Reader", "unique", false, false, "ref §3: Reader Writer Alloc unique, no, no"},
	{"Writer", "unique", false, false, "ref §3"},
	{"Alloc", "unique", false, false, "ref §3"},
	{"Io", "plain", true, true, "ref §3: Io plain, yes, yes"},
	{"&Scope", "plain", true, true, "ref §3: &Scope plain, yes, yes"},
	{"atomic.AtomicInt", "unique", true, true, "plan 3.2: unique, not a view, Send, Share"},
	{"atomic.AtomicBool", "unique", true, true, "plan 3.2"},
	{"time.Duration", "plain", true, true, "plan 3.2; ref §18: Duration is plain"},
	{"time.Instant", "plain", true, true, "plan 3.2"},
	{"Limits", "plain", true, true, "plan 3.2: as §18, plain, both"},
	{"StackSize", "plain", true, true, "plan 3.2"},
	{"TargetInfo", "plain", true, true, "plan 3.2"},
	{"Arch", "plain", true, true, "plan 3.2"},
	{"Os", "plain", true, true, "plan 3.2"},
	{"proc.Signal", "plain", true, true, "plan 3.2"},
	{"Hasher", "plain", true, true, "plan 3.2: Hasher plain, both"},
	{"OutOfMemory", "plain", true, true, "plan 3.2: prelude errors are payload-free"},
	{"WriteError", "plain", true, true, "plan 3.2"},
	{"fs.Error", "plain", true, true, "plan 3.2: fs.Os(code: i64)"},
	{"net.Error", "plain", true, true, "plan 3.2"},
}

const kindImports = "import std.fs\nimport std.net\nimport std.time\nimport std.proc\nimport std.sync\nimport std.atomic\n\n"

// kindBoundsProgram returns the bounds program and, per row, the line of
// its NeedPlain, NeedSend and NeedShare calls.
func kindBoundsProgram(rows []kindRow) (string, [][3]int) {
	var b strings.Builder
	b.WriteString(kindImports)
	b.WriteString("NeedPlain[T: Plain]() {\n}\n\nNeedSend[T: Send]() {\n}\n\nNeedShare[T: Share]() {\n}\n\nProbe() {\n")
	line := strings.Count(b.String(), "\n") + 1
	lines := make([][3]int, len(rows))
	for i, r := range rows {
		for j, f := range []string{"NeedPlain", "NeedSend", "NeedShare"} {
			fmt.Fprintf(&b, "    %s[%s]()\n", f, r.ty)
			lines[i][j] = line
			line++
		}
	}
	b.WriteString("}\n\nexport Main(sys: &System) {\n}\n")
	return b.String(), lines
}

// kindDropProgram returns the drop program and, per row, the line of D's
// declaration, where E0402 is reported.
func kindDropProgram(rows []kindRow) (string, []int) {
	var b strings.Builder
	b.WriteString(kindImports)
	lines := make([]int, len(rows))
	for i, r := range rows {
		lines[i] = strings.Count(b.String(), "\n") + 1
		fmt.Fprintf(&b, "D%d(x: %s) {\n}\n\n", i, r.ty)
	}
	b.WriteString("export Main(sys: &System) {\n}\n")
	return b.String(), lines
}

// kindCodes maps a line of main.mk to its diagnostic codes, and fails the
// test on any diagnostic outside want.
func kindCodes(t *testing.T, out string, want ...string) map[int][]string {
	t.Helper()
	got := map[int][]string{}
	for _, lc := range diagCodes(out) {
		f := strings.Fields(lc)
		n, _ := strconv.Atoi(f[0])
		got[n] = append(got[n], f[1])
		ok := false
		for _, w := range want {
			ok = ok || f[1] == w
		}
		if !ok {
			t.Errorf("unexpected %s at main.mk:%d\n%s", f[1], n, out)
		}
	}
	return got
}

// checkUncapped runs mk check on src over a copy of std with no cap on the
// number of diagnostics (text output stops at 50 by default).
func checkUncapped(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := mkCmd(t, root, stdDir, "check", "--max-diagnostics", "0", "main.mk")
	return out
}

func hasCode(codes []string, c string) bool {
	for _, x := range codes {
		if x == c {
			return true
		}
	}
	return false
}

// TestStdKindsPinned: every row of kindRows has the kind, Send and
// Share the reference states, as check sees it from a user module. Two
// programs per run:
//
//   - the bounds program calls NeedPlain[T](), NeedSend[T]() and
//     NeedShare[T]() on a line each; E0701 on a line means T lacks that bound;
//   - the drop program declares D(x: T) {} per type; E0402 at D means T is
//     linear (a unique or plain parameter may go unused).
//
// A type that is neither Plain nor linear is unique. `view` has no probe that
// tells it apart without a body, so it is not pinned here.
func TestStdKindsPinned(t *testing.T) {
	t.Parallel()
	src, blines := kindBoundsProgram(kindRows)
	out := checkUncapped(t, src)
	bc := kindCodes(t, out, "E0701")
	dsrc, dlines := kindDropProgram(kindRows)
	dout := checkUncapped(t, dsrc)
	dc := kindCodes(t, dout, "E0402")
	for i, r := range kindRows {
		plain := !hasCode(bc[blines[i][0]], "E0701")
		send := !hasCode(bc[blines[i][1]], "E0701")
		share := !hasCode(bc[blines[i][2]], "E0701")
		linear := hasCode(dc[dlines[i]], "E0402")
		kind := "unique"
		switch {
		case plain && linear:
			kind = "plain and linear"
		case plain:
			kind = "plain"
		case linear:
			kind = "linear"
		}
		if kind != r.kind || send != r.send || share != r.share {
			t.Errorf("%s: kind %s, Send %v, Share %v; want %s, %v, %v (%s)", r.ty, kind, send, share, r.kind, r.send, r.share, r.why)
		}
	}
}

// TestPrivateStdFieldNeedsView: a private field of
// a std type makes an exported struct need `view` (E0410 with needs
// ["view"]), for List and for AtomicInt alike: the rule that a private
// field counts as exported covers raw.Atomic fields only, not a std struct
// that holds one.
func TestPrivateStdFieldNeedsView(t *testing.T) {
	t.Parallel()
	src := "import std.atomic\n\nexport struct X {\n    l: List[int]\n}\n\nexport struct Y {\n    a: atomic.AtomicInt\n}\n\nexport Main(sys: &System) {\n}\n"
	out := checkUncapped(t, src)
	got := kindCodes(t, out, "E0410")
	for _, line := range []int{3, 7} {
		if !hasCode(got[line], "E0410") {
			t.Errorf("no E0410 at main.mk:%d\n%s", line, out)
		}
	}
	if strings.Count(out, `needs: ["view"]`) != 2 {
		t.Errorf("want two E0410 needing view\n%s", out)
	}
}

// scopeLayoutProgram fails Pinned (E0901 bounds) when a layout fact of
// TestScopeLayout does not hold; Ran always fails, which shows the
// checks were evaluated.
const scopeLayoutProgram = `import std.fs

Up(n: int, a: int): int {
    return (n + a - 1) / a * a
}

Fails(): int {
    var n := 0
    if SizeOf[Scope]() != 128 {
        n += 1
    }
    if AlignOf[Scope]() != 8 {
        n += 1
    }
    if SizeOf[Scope[OutOfMemory]]() != Up(128 + SizeOf[OutOfMemory!void](), AlignOf[Scope[OutOfMemory]]()) {
        n += 1
    }
    if SizeOf[Scope[fs.Error]]() != Up(128 + SizeOf[fs.Error!void](), AlignOf[Scope[fs.Error]]()) {
        n += 1
    }
    if SizeOf[fs.Error!void]() != 16 {
        n += 1
    }
    return n
}

const Pinned = [0; 1][Fails()]
const Ran = [0; 1][Fails() + 1]

export Main(sys: &System) {
}
`

// TestScopeLayout: check's layout of Scope is 128 bytes at alignment
// 8, and Scope[E] is 128 plus SizeOf[E!void], rounded to its alignment, for
// a payload-free error and for fs.Error (whose Os carries an i64), as the
// runtime's Scope layout fixes. Not pinned here:
// that err sits at offset 128 (no user code can name a Scope[E]'s fields),
// and Freeze, which has no user-visible bound.
func TestScopeLayout(t *testing.T) {
	t.Parallel()
	out := checkUncapped(t, scopeLayoutProgram)
	got := kindCodes(t, out, "E0901")
	if hasCode(got[28], "E0901") && len(got) == 1 {
		return
	}
	t.Errorf("want only the witness E0901 at main.mk:28 (Pinned fails when a Scope layout fact is wrong)\n%s", out)
}
