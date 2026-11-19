// Why: mk query gives an editor or an agent one short answer per question,
// so every query kind must answer correctly within its token cap.
// What: tests of each query's answer over a small package, the 200-token
// cap, not-found answers, body queries (panics, allocs), partial answers
// when the check stops early, and BenchmarkQueryWarm.
// How: writes querySource to a temporary package and runs queryOne for each
// query, matching lines of the answer.

package driver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const querySource = `import std.fs

/// A point on the grid.
export struct Point {
    export x: int
    y: int
}

export Point.Sum(&self): int {
    return self.x + self.y
}

export struct Holder {
    f: fs.File
}

export Pick(a: &Point, b: &Point): &Point from a {
    return a
}

export Load(io: Io, p: str): fs.Error|OutOfMemory!int {
    a := Arena.New(io.heap.Alloc())
    defer a.Free()
    t := try fs.ReadText(&a, io, p)
    return t.Len()
}

export Main(sys: &System) {
    _ = Pick(&Point(x: 1, y: 2), &Point(x: 3, y: 4)).Sum()
}
`

func queryOne(t *testing.T, dir, q string, args ...string) *QueryAnswer {
	t.Helper()
	s, err := NewSession(Config{Root: filepath.Join(dir, "q.mk"), CacheDir: filepath.Join(dir, ".cache")})
	if err != nil {
		t.Fatal(err)
	}
	a, res, err := s.Query(q, args)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatalf("query %s %v: no answer:\n%s", q, args, res.Report.Text(res.Sources))
	}
	return a
}

func answerHas(t *testing.T, a *QueryAnswer, want ...string) {
	t.Helper()
	text := a.Text()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s %s: answer lacks %q:\n%s", a.Query, a.Subject, w, text)
		}
	}
	if len(text) > QueryCapBytes+80 {
		t.Errorf("%s %s: %d bytes, over the cap", a.Query, a.Subject, len(text))
	}
}

// TestQuery_Answers: members, sig (with flows, error set and doc), import,
// modules, kind, shape, flows, errors and find, each over the package or std.
func TestQuery_Answers(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": querySource})
	answerHas(t, queryOne(t, dir, "members", "Point"), "export x: int", "y: int", "export Sum(&self): int")
	answerHas(t, queryOne(t, dir, "sig", "Pick"), "export Pick(a: &Point, b: &Point): &Point from a")
	answerHas(t, queryOne(t, dir, "sig", "Point"), "export struct Point", "/// A point on the grid.")
	answerHas(t, queryOne(t, dir, "sig", "Load"), "export Load(io: Io, p: str): ", "!int")
	answerHas(t, queryOne(t, dir, "sig", "Map.Get"), "Map.Get(&self, k: &K): ?&V")
	answerHas(t, queryOne(t, dir, "import", "ReadText"), "import std.fs", "spelled fs.ReadText")
	answerHas(t, queryOne(t, dir, "import", "Print"), "prelude: no import")
	answerHas(t, queryOne(t, dir, "modules"), "main (1 files", "std: ", "std.fs")
	answerHas(t, queryOne(t, dir, "kind", "Point"), "Point is plain")
	answerHas(t, queryOne(t, dir, "kind", "Holder"), "Holder is linear", "field f: File is linear")
	answerHas(t, queryOne(t, dir, "shape", "Point"), "size 16, align 8", "Send yes, Share yes")
	answerHas(t, queryOne(t, dir, "flows", "Pick"), "from a: the result holds the loan; a stays borrowed (declared)")
	answerHas(t, queryOne(t, dir, "flows", "Point.Sum"), "result holds no loan")
	answerHas(t, queryOne(t, dir, "errors", "Load"), "OutOfMemory", "NotFound  std.fs")
	answerHas(t, queryOne(t, dir, "errors", "Point.Sum"), "cannot fail")
	answerHas(t, queryOne(t, dir, "find", "size"), "List.Len")
	answerHas(t, queryOne(t, dir, "find", "HashMap"), "Map ")
}

// TestQuery_NotFound: an unknown name answers found false with the nearest
// names; an unknown query or a wrong argument count is a usage error.
func TestQuery_NotFound(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": querySource})
	a := queryOne(t, dir, "sig", "Pik")
	if a.Found || !strings.Contains(a.Text(), "no item named Pik") || !strings.Contains(a.Text(), "Pick") {
		t.Errorf("not found: %+v", a)
	}
	if !strings.HasPrefix(string(a.JSON()), `{"query":"sig","subject":"Pik","found":false,"answer":["no item named Pik"`) {
		t.Errorf("json: %s", a.JSON())
	}
	s, _ := NewSession(Config{Root: filepath.Join(dir, "q.mk"), CacheDir: t.TempDir()})
	var ue *UsageError
	for _, c := range [][]string{{"frobnicate", "x"}, {"sig"}, {"modules", "x"}} {
		if _, _, err := s.Query(c[0], c[1:]); !errors.As(err, &ue) {
			t.Errorf("%q: err %v", c, err)
		}
	}
}

// TestQueryCap: an answer longer than the cap keeps its first lines and
// counts the rest.
func TestQueryCap(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "a line of about forty bytes, numbered "+itoaQ(i))
	}
	a := &QueryAnswer{}
	a.capLines(lines)
	n := 0
	for _, l := range a.Answer {
		n += len(l) + 1
	}
	if n > QueryCapBytes || a.Truncated != 100-len(a.Answer) || a.Truncated == 0 || !strings.Contains(a.Text(), "more (capped at 200 tokens)") {
		t.Errorf("%d bytes, %d kept, %d cut", n, len(a.Answer), a.Truncated)
	}
}

// bodySource has a known call graph: Main -> Mid -> Leaf, Main -> Fact
// (recursive), Main -> Grow -> Arena.Slice, and a loan of xs held by r.
const bodySource = `Fact(n: int): int {
    if n <= 1 {
        return 1
    }
    return n * Fact(n - 1)
}

Leaf(xs: []int, i: int): int {
    Assert(i >= 0)
    return xs[i] + 1
}

Mid(xs: []int): int {
    return Leaf(xs, 0) + Leaf(xs, 1)
}

Grow(a: &Arena, n: int): OutOfMemory!int {
    s := try a.Slice[int](n)
    s[0] = 1
    return s.Len()
}

export Main(sys: &System): OutOfMemory!void {
    xs := [1, 2, 3]
    r := &xs
    y := Mid(r[0..2])
    _ = Fact(y)
    _ = try Grow(sys.arena, 4)
    _ = r
}
`

// TestQuery_BodyQueries: callers, panics, allocs, stack and why-live over
// the lowered bodies, cold and then from a warm cache (a module hit, whose
// bodies are its function results).
func TestQuery_BodyQueries(t *testing.T) {
	dir := testPkg(t, map[string]string{"q.mk": bodySource})
	for _, pass := range []string{"cold", "warm"} {
		t.Run(pass, func(t *testing.T) {
			answerHas(t, queryOne(t, dir, "callers", "Leaf"), "Mid  q.mk:14")
			answerHas(t, queryOne(t, dir, "callers", "Mid"), "Main  q.mk:26")
			answerHas(t, queryOne(t, dir, "callers", "Main"), "no direct caller of Main in the package")
			answerHas(t, queryOne(t, dir, "callers", "Arena.Slice"), "(callers inside std are not listed)")
			answerHas(t, queryOne(t, dir, "panics", "Leaf"), "Leaf reaches 1 function(s)", "assert  q.mk:9  in Leaf",
				"bounds  q.mk:10  in Leaf", "checks that trap: overflow 1, bounds 1")
			// A site reached through calls names the call path from F.
			answerHas(t, queryOne(t, dir, "panics", "Main"), "assert  q.mk:9  in Main -> Mid (q.mk:26) -> Leaf (q.mk:14)",
				"bounds  q.mk:19  in Main -> Grow (q.mk:28)")
			answerHas(t, queryOne(t, dir, "allocs", "Main"), "Main allocates: Main -> Grow (q.mk:28) -> Arena.Slice (q.mk:18)")
			answerHas(t, queryOne(t, dir, "allocs", "Mid"), "none: no chain of direct calls from Mid")
			answerHas(t, queryOne(t, dir, "stack", "Main"), "no static bound: recursion Fact -> Fact, reached by Main -> Fact")
			answerHas(t, queryOne(t, dir, "stack", "Mid"), "bounded: no recursion", "deepest direct chain: 2 frame(s): Mid -> Leaf")
			answerHas(t, queryOne(t, dir, "why-live", "Main", "26"), "&xs (line 25) held by r", "used until line 29")
			answerHas(t, queryOne(t, dir, "why-live", "Leaf", "9"), "no loan of Leaf is live at line 9")
			if a := queryOne(t, dir, "why-live", "Main", "x"); !strings.Contains(a.Text(), "takes a line number") {
				t.Errorf("why-live Main x: %s", a.Text())
			}
			if a := queryOne(t, dir, "allocs", "Main"); !strings.Contains(string(a.JSON()), "Main -> Grow") {
				t.Errorf("JSON escapes '>': %s", a.JSON())
			}
		})
	}
}

// BenchmarkQueryWarm is one warm `mk query sig` in process: a session's
// Check from a warm module cache, then the answer (what one process
// start costs, without the process).
func BenchmarkQueryWarm(b *testing.B) {
	dir := writeTree(b, map[string]string{"q.mk": querySource})
	cfg := Config{Root: filepath.Join(dir, "q.mk"), CacheDir: filepath.Join(dir, ".cache")}
	for i := 0; i < b.N+1; i++ {
		if i == 1 {
			b.ResetTimer()
		}
		s, err := NewSession(cfg)
		if err != nil {
			b.Fatal(err)
		}
		if a, _, err := s.Query("sig", []string{"Pick"}); err != nil || a == nil || !a.Found {
			b.Fatalf("sig Pick: %v %v", a, err)
		}
	}
}

// TestQuery_PartialWhenCheckStopsEarly: a package that does not check as far
// as types (a directory program with no Main is a resolve error) still gets
// an answer from its declarations, marked partial; a clean one is not.
func TestQuery_PartialWhenCheckStopsEarly(t *testing.T) {
	dir := testPkg(t, map[string]string{"lib.mk": "export Sum(a: int, b: int): int {\n    return a + b\n}\n"})
	s, err := NewSession(Config{Root: dir, CacheDir: filepath.Join(t.TempDir(), "c")})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Query("sig", []string{"Sum"})
	if err != nil || a == nil {
		t.Fatalf("sig Sum: %v %v", a, err)
	}
	if !a.Partial || !strings.Contains(a.Text(), "(partial") || !strings.Contains(string(a.JSON()), `"partial":true`) {
		t.Errorf("want a partial answer: %+v\n%s", a, a.Text())
	}
	clean := queryOne(t, writeTree(t, map[string]string{"q.mk": querySource}), "sig", "Pick")
	if clean.Partial || strings.Contains(string(clean.JSON()), "partial") {
		t.Errorf("clean package answered partial: %+v", clean)
	}
}
