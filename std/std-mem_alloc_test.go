// Why: std.mem's allocators back every container that takes one, so their
// placement and accounting must match a known-good model.
// What: mem.Fixed and mem.Counting against the Go models of
// std-mem_allocharness_test.go, and the containers over Fixed, Counting, Null.
// How: generated operation sequences, run dev and release, compared line by line.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// fixedCase is one buffer of the differential test: its length, the offset
// into a 16-aligned-or-not stack array (so the skew varies) and the ops.
type fixedCase struct {
	name     string
	size     int
	shift    int
	seed     uint64
	ops      int
	maxSize  int
	noResets bool
	wild     bool // also bad requests and frees and resizes with other numbers (memWildOps)
}

var fixedCases = []fixedCase{
	{"empty", 0, 0, 1, 40, 8, false, false},
	{"one", 1, 3, 2, 60, 2, false, false},
	{"seven", 7, 1, 3, 80, 9, false, false},
	{"small", 64, 5, 4, 300, 40, false, false},
	{"page", 4096, 0, 5, 1500, 300, false, false},
	{"page-skew", 4096, 7, 6, 1500, 300, false, false},
	{"page-noreset", 4096, 2, 7, 1500, 120, true, false},
	{"big", 65536, 4, 8, 3000, 300, true, false},
	{"wild", 256, 3, 9, 1500, 48, false, true},
	{"wild-page", 4096, 6, 10, 2000, 200, true, true},
}

// fixedProgram runs every case in one program: each case's buffer is a
// slice of one array, at its shift.
func fixedProgram(cases []fixedCase) (string, map[string][]int) {
	var b strings.Builder
	b.WriteString("import std.memfix\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	ops := map[string][]int{}
	for i, c := range cases {
		o := memOps(c.seed, c.ops, c.maxSize, !c.noResets)
		if c.wild {
			o = memWildOps(c.seed, c.ops, c.maxSize, !c.noResets)
		}
		ops[c.name] = o
		fmt.Fprintf(&b, "    try Print(mut out, \"case %s\\n\")\n", c.name)
		fmt.Fprintf(&b, "    var buf%d: [%d]u8 = [0; %d]\n", i, c.size+c.shift+1, c.size+c.shift+1)
		fmt.Fprintf(&b, "    ops%d := %s\n", i, mkInts(o))
		fmt.Fprintf(&b, "    try memfix.RunFixed(mut out, mut buf%d[%d..%d], ops%d[..])\n", i, c.shift, c.shift+c.size, i)
	}
	b.WriteString("}\n")
	return b.String(), ops
}

// splitCases cuts a run's output at its "case <name>" lines.
func splitCases(out string) map[string]string {
	res := map[string]string{}
	var name string
	var cur strings.Builder
	for _, l := range strings.SplitAfter(out, "\n") {
		if n, ok := strings.CutPrefix(l, "case "); ok {
			if name != "" {
				res[name] = cur.String()
			}
			name = strings.TrimSpace(n)
			cur.Reset()
			continue
		}
		cur.WriteString(l)
	}
	if name != "" {
		res[name] = cur.String()
	}
	return res
}

// TestSTDMEM_FixedMatchesModel: seeded sequences of allocations (0 to 300
// bytes, alignments 1 to 4096), resizes of the last and other blocks, frees
// and resets over Fixed buffers of 0, 1, 7, 64, 4096 and 65536 bytes at
// several address skews print exactly the Go model's offsets; the model
// itself checks every block is aligned, in bounds and disjoint. Dev and
// release. Every block the run hands out starts on an 8-byte boundary.
func TestSTDMEM_FixedMatchesModel(t *testing.T) {
	t.Parallel()
	src, ops := fixedProgram(fixedCases)
	for _, flags := range [][]string{nil, {"--release"}} {
		out := collExec(t, memBuild(t, src, flags...))
		got := splitCases(out)
		for _, c := range fixedCases {
			g, ok := got[c.name]
			if !ok {
				t.Fatalf("%v: no output for case %s\n%s", flags, c.name, out)
			}
			var skew int
			if _, err := fmt.Sscanf(g, "skew %d\n", &skew); err != nil {
				t.Fatalf("%v %s: no skew line: %q", flags, c.name, g)
			}
			want := fixedExpect(t, skew, c.size, ops[c.name])
			if g != want {
				t.Errorf("%v case %s (skew %d):", flags, c.name, skew)
				diffLines(t, g, want)
			}
			// The reference's rule, judged on the Mk run alone: every
			// block starts on an 8-byte boundary of the address space.
			for _, l := range strings.Split(g, "\n") {
				var off int
				if _, err := fmt.Sscanf(l, "a %d", &off); err == nil && (skew+off)%8 != 0 {
					t.Errorf("%v case %s (skew %d): block at offset %d is not on an 8-byte boundary", flags, c.name, skew, off)
				}
			}
		}
	}
}

// TestSTDMEM_CountingMatchesModel: the same op sequences through a Counting
// over the test heap, over a Fixed, and over Null print the counts the model
// predicts after every op, and zero bytes and live allocations at the end;
// over a Fixed and over Null the model also predicts every result.
func TestSTDMEM_CountingMatchesModel(t *testing.T) {
	t.Parallel()
	heapOps := memOps(11, 1500, 300, false)
	fixedOps := memOps(12, 1500, 200, false)
	nullOps := memOps(13, 200, 64, false)
	src := fmt.Sprintf(`import std.mem
import std.memfix

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    h := %s
    try Print(mut out, "case heap\n")
    try memfix.RunCounting(mut out, sys.heap.Alloc(), h[..])
    var buf: [4097]u8 = [0; 4097]
    try Print(mut out, "case fixed\n")
    try Print(mut out, "skew {s}\n", (s: memfix.Skew(buf[1..])))
    f := mem.Fixed.New(mut buf[1..])
    x := %s
    try memfix.RunCounting(mut out, f.Alloc(), x[..])
    try Print(mut out, "case null\n")
    z := %s
    try memfix.RunCounting(mut out, mem.Null(), z[..])
}
`, mkInts(heapOps), mkInts(fixedOps), mkInts(nullOps))
	for _, flags := range [][]string{nil, {"--release"}} {
		out := collExec(t, memBuild(t, src, flags...))
		got := splitCases(out)
		if g := got["heap"]; g != countingExpect(t, heapOps, g, nil, false) {
			t.Errorf("%v heap:", flags)
			diffLines(t, g, countingExpect(t, heapOps, g, nil, false))
		}
		fx := got["fixed"]
		var skew int
		if _, err := fmt.Sscanf(fx, "skew %d\n", &skew); err != nil {
			t.Fatalf("%v fixed: no skew line: %q", flags, fx)
		}
		fx = fx[strings.IndexByte(fx, '\n')+1:]
		if want := countingExpect(t, fixedOps, fx, newFixedModel(skew, 4096), false); fx != want {
			t.Errorf("%v fixed:", flags)
			diffLines(t, fx, want)
		}
		if g, want := got["null"], countingExpect(t, nullOps, got["null"], nil, true); g != want {
			t.Errorf("%v null:", flags)
			diffLines(t, g, want)
		}
	}
}

// TestSTDMEM_SameOwner: Alloc on one Fixed or Counting, and mem.Null, each
// return one owner every call (the reference's rule for Heap.Alloc, which
// the heap containers' ownership check relies on), dev and release; and a
// HeapList made with mem.Null() in main.mk passes the ownership check of a Free
// in another module (memfix), so the static owner is one across modules.
func TestSTDMEM_SameOwner(t *testing.T) {
	t.Parallel()
	src := `import std.mem
import std.memfix

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var buf: [64]u8 = [0; 64]
    try Print(mut out, "{s}\n", (s: memfix.SameOwner(mut buf[..], sys.heap.Alloc())))
    memfix.FreeNull(HeapList[int].New(mem.Null()))
    try Print(mut out, "freed\n")
}
`
	for _, flags := range [][]string{nil, {"--release"}} {
		if out := collExec(t, memBuild(t, src, flags...)); out != "true\nfreed\n" {
			t.Errorf("%v: same owner: %q", flags, out)
		}
	}
}
