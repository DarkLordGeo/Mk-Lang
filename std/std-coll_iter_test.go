// Why: the text iterators in core_iter.mk scan a word at a time, so they need
// checks against plain Go rules at every offset.
// What: TestStdColl_IteratorsMatchGo (IndexByteFrom, Lines, Split, Words) and
// TestStdColl_RecursiveHeapContainers.
// How: IndexByteFrom against strings.IndexByte from every offset; the
// iterators over seeded text with CR, LF and multi-byte scalars against Go.

package std

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"
)

// iterCases are seeded strings over an alphabet that exercises every
// branch: the searched bytes, CR, LF, spaces, a two-byte scalar (é: C3 A9),
// a three-byte one (€: E2 82 AC) and U+3000 (White_Space, three bytes),
// with lengths around the 8- and 16-byte word boundaries.
func iterCases() []string {
	r := rand.New(rand.NewSource(7))
	alphabet := []string{"a", "b", ",", ";", "\n", "\r", " ", "\t", "é", "€", "　", ",;"}
	var cases []string
	for _, n := range []int{0, 1, 2, 7, 8, 9, 15, 16, 17, 23, 24, 25, 31, 33, 40, 63, 64, 65, 100} {
		for k := 0; k < 4; k++ {
			var b strings.Builder
			for b.Len() < n {
				// Long runs of one letter make the word scan cross whole words.
				if r.Intn(3) == 0 {
					b.WriteString(strings.Repeat("x", r.Intn(20)))
					continue
				}
				b.WriteString(alphabet[r.Intn(len(alphabet))])
			}
			cases = append(cases, b.String())
		}
	}
	return cases
}

// goLinesModel is std's Lines: split at \n, strip one \r before
// it, a final \n ends the last line, "" has no lines, a lone \r stays.
func goLinesModel(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		l := s[:i]
		if strings.HasSuffix(l, "\r") {
			l = l[:len(l)-1]
		}
		out = append(out, l)
		s = s[i+1:]
	}
	return out
}

// goWordsModel is std's Words: maximal runs of non-White_Space scalars.
func goWordsModel(s string) []string {
	return strings.FieldsFunc(s, unicode.IsSpace)
}

func lens(xs []string) string {
	var b strings.Builder
	for _, x := range xs {
		fmt.Fprintf(&b, " %d", len(x))
	}
	return b.String()
}

// TestStdColl_IteratorsMatchGo: for every case string, IndexByteFrom of
// ',', '\n', 0xA9 (a continuation byte) and 'x' from every offset 0 to
// Len() + 1, and the piece lengths of Lines, Split(","), Split(",;") and
// Words, equal the Go models'.
func TestStdColl_IteratorsMatchGo(t *testing.T) {
	t.Parallel()
	cases := iterCases()
	var src strings.Builder
	fmt.Fprintf(&src, "import std.collfix\n\nconst Cases: [%d]str = [", len(cases))
	for i, c := range cases {
		if i > 0 {
			src.WriteString(",\n    ")
		}
		src.WriteString(mkString(c))
	}
	src.WriteString(`]

const Bytes: [4]u8 = [0x2C, 0x0A, 0xA9, 0x78]

Pieces(out: mut Writer, tag: str, it: mut Split): WriteError!void {
    try Print(out, "{tag}", (tag))
    for p := it.Next() {
        n := p.Len()
        try Print(out, " {n}", (n))
    }
    try Print(out, "\n", ())
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for c in Cases {
        for b in Bytes {
            try Print(mut out, "i", ())
            for from in 0..c.Len() + 2 {
                x := collfix.IndexByte(c, b, from)
                try Print(mut out, " {x}", (x))
            }
            try Print(mut out, "\n", ())
        }
        try Print(mut out, "l", ())
        var ls := c.Lines()
        for l := ls.Next() {
            n := l.Len()
            try Print(mut out, " {n}", (n))
        }
        try Print(mut out, "\n", ())
        var s1 := c.Split(",")
        try Pieces(mut out, "s", mut s1)
        var s2 := c.Split(",;")
        try Pieces(mut out, "t", mut s2)
        try Print(mut out, "w", ())
        var ws := c.Words()
        for w := ws.Next() {
            n := w.Len()
            try Print(mut out, " {n}", (n))
        }
        try Print(mut out, "\n", ())
    }
}
`)
	var want strings.Builder
	for _, c := range cases {
		for _, b := range []byte{',', '\n', 0xA9, 'x'} {
			want.WriteString("i")
			for from := 0; from <= len(c)+1; from++ {
				x := -1
				if from <= len(c) {
					if j := strings.IndexByte(c[from:], b); j >= 0 {
						x = from + j
					}
				}
				fmt.Fprintf(&want, " %d", x)
			}
			want.WriteString("\n")
		}
		fmt.Fprintf(&want, "l%s\n", lens(goLinesModel(c)))
		fmt.Fprintf(&want, "s%s\n", lens(strings.Split(c, ",")))
		fmt.Fprintf(&want, "t%s\n", lens(strings.Split(c, ",;")))
		fmt.Fprintf(&want, "w%s\n", lens(goWordsModel(c)))
	}
	diffLines(t, collRun(t, src.String()), want.String())
}

// TestStdColl_RecursiveHeapContainers: a type may hold heap containers of
// itself (a tree's children, a graph's adjacency), because HeapList,
// HeapMap and TextMap name their element types through [0]&T markers, which
// need no layout of T; with [0]T markers each of these is E0907
// layout-cycle. The program builds a two-level tree and frees it, and a
// graph, through their impl Free and the containers' deep Free.
func TestStdColl_RecursiveHeapContainers(t *testing.T) {
	t.Parallel()
	got := collRun(t, `linear struct Tree {
    v: int
    kids: HeapList[Tree]
}

linear struct Graph {
    edges: HeapMap[int, Graph]
    names: TextMap[Graph]
}

impl Free for Tree {
    Free(self, a: &Alloc) {
        Tree(v: _, kids) := self
        kids.Free(a)
    }
}

impl Free for Graph {
    Free(self, a: &Alloc) {
        Graph(edges, names) := self
        edges.Free(a)
        names.Free(a)
    }
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    a := sys.heap.Alloc()
    var root := Tree(v: 1, kids: HeapList[Tree].New(a))
    for i in 0..3 {
        kid := Tree(v: i, kids: HeapList[Tree].New(a))
        root.kids.Push(a, kid) else {
            kid.Free(a)
            root.Free(a)
            fail OutOfMemory
        }
    }
    n := root.kids.Len()
    root.Free(a)
    g := Graph(edges: HeapMap[int, Graph].New(a), names: TextMap[Graph].New(a))
    g.Free(a)
    try Print(mut out, "kids {n}\n", (n))
}
`)
	if got != "kids 3\n" {
		t.Errorf("stdout %q, want %q", got, "kids 3\n")
	}
}
