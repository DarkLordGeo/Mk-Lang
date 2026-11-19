// Why: the language server asks the driver many questions per edit; each
// must answer from the last Check, never run a second one.
// What: TestLSPAPI, TestQueryChecked_SameAsQuery and TestQueryChecked_Partial,
// over lspSession and lspCheckAll.
// How: checks a small two-file package once with ReadFile counted, cold and
// warm, then asks Items, Lookup, UsesOf, TypeAt and QueryChecked (lspapi.go).

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"mk/internal/source"
)

var lspFiles = map[string]string{
	"a.mk": `/// A point on the grid.
export struct Point {
    export x: int
    y: int
}

export Point.Sum(&self): int {
    return self.x + self.y
}

export Point.New(x: int, y: int): Point {
    return Point(x: x, y: y)
}

enum Shape {
    Dot
    Line
}
`,
	"b.mk": `export Main(sys: &System) {
    p := Point.New(1, 2)
    q := p
    n := q.Sum()
    s := "abc"
    k := s.Len()
    _ = n + k
}

Apply(f: fn(int): int): int {
    return f(1)
}

Twice(): int {
    return Apply(fn(x: int): int {
        y := x * 2
        return y
    })
}
`,
}

// lspSession checks dir once with ReadFile counted, and returns the
// session, its result and the counter.
func lspSession(t *testing.T, dir, cache string, keep bool) (*Session, Result, *atomic.Int64) {
	t.Helper()
	reads := &atomic.Int64{}
	s, err := NewSession(Config{Root: dir, CacheDir: cache, KeepSyntax: keep, ReadFile: func(p string) ([]byte, error) {
		reads.Add(1)
		return os.ReadFile(p)
	}})
	if err != nil {
		t.Fatal(err)
	}
	res := s.Check()
	if len(res.Report.Diags) > 0 {
		t.Fatalf("check:\n%s", res.Report.Text(res.Sources))
	}
	if reads.Load() == 0 {
		t.Fatal("Check read no file through Config.ReadFile: the counter cannot see a second check")
	}
	return s, res, reads
}

// srcPath is the Result.Sources path of the package file named base.
func srcPath(t *testing.T, res Result, base string) string {
	t.Helper()
	for _, f := range res.Sources.Files() {
		if strings.HasSuffix(f.Path, "/"+base) || f.Path == base {
			return f.Path
		}
	}
	t.Fatalf("no source %s", base)
	return ""
}

// offAfter is the offset just past the first occurrence of what after the
// first occurrence of anchor in file base.
func offAfter(t *testing.T, res Result, path, anchor, what string) uint32 {
	t.Helper()
	f, _ := res.Sources.ByPath(path)
	a := strings.Index(f.Text, anchor)
	if a < 0 {
		t.Fatalf("%s: no %q", path, anchor)
	}
	i := strings.Index(f.Text[a:], what)
	if i < 0 {
		t.Fatalf("%s: no %q after %q", path, what, anchor)
	}
	return uint32(a + i + len(what))
}

func TestLSPAPI(t *testing.T) {
	dir := writeTree(t, lspFiles)
	cache := t.TempDir()
	for _, run := range []struct {
		name       string
		keep       bool
		warmSyntax bool // the warm run keeps the package's syntax
	}{{"plain", false, false}, {"keepsyntax", true, true}} {
		for _, warm := range []bool{false, true} {
			name := run.name + map[bool]string{false: "/cold", true: "/warm"}[warm]
			t.Run(name, func(t *testing.T) {
				c := filepath.Join(cache, run.name)
				if !warm {
					os.RemoveAll(c)
				}
				s, res, reads := lspSession(t, dir, c, run.keep)
				if warm && run.keep {
					// KeepSyntax re-checks the package's signatures, not its
					// bodies: a function is still a function-result hit
					// (one holding a fn literal, Twice, is never reused).
					var hit []string
					for _, k := range res.Trace.ItemsFromCache {
						hit = append(hit, k.String())
					}
					if !slices.Contains(hit, "main.Main") || !slices.Contains(hit, "main.Point:Sum") {
						t.Errorf("warm KeepSyntax check: units reused %q, checked %v", hit, res.Trace.ItemsChecked)
					}
				}
				w0, n0 := s.w, reads.Load()
				syntax := !warm || run.warmSyntax
				lspCheckAll(t, s, res, syntax)
				if s.w != w0 || reads.Load() != n0 {
					t.Errorf("an API checked again: world changed %v, files read %d more", s.w != w0, reads.Load()-n0)
				}
			})
		}
	}
}

// lspCheckAll asks each API; syntax says whether the package's modules
// have their syntax in the session (TypeAt and the method uses need it).
func lspCheckAll(t *testing.T, s *Session, res Result, syntax bool) {
	a, b := srcPath(t, res, "a.mk"), srcPath(t, res, "b.mk")

	// Items: source order, methods and fields under their type.
	var got []string
	for _, it := range s.Items(a) {
		got = append(got, it.Kind+" "+it.Owner+"."+it.Name+" "+itoaQ(int(it.Def.Line))+":"+itoaQ(int(it.Def.Col))+"-"+itoaQ(int(it.End.Col)))
	}
	want := []string{"struct .Point 2:15-20", "field Point.x 3:12-13", "field Point.y 4:5-6",
		"func Point.Sum 7:14-17", "func Point.New 11:14-17", "enum .Shape 15:6-11", "variant Shape.Dot 16:5-8", "variant Shape.Line 17:5-9"}
	if !slices.Equal(got, want) {
		t.Errorf("Items(a.mk):\n got %q\nwant %q", got, want)
	}
	if its := s.Items(b); len(its) != 3 || its[0].Name != "Main" || its[0].Kind != "func" || !its[0].Exported ||
		its[1].Name != "Apply" || its[2].Name != "Twice" || its[2].Exported {
		t.Errorf("Items(b.mk) = %+v (the fn literal is not an item of its own)", its)
	}
	if its := s.Items("no/such.mk"); its != nil {
		t.Errorf("Items of an unknown file = %+v", its)
	}

	// Lookup: a package method, a std method, std's declaration exact.
	if it, ok := s.Lookup("Point.Sum"); !ok || it.Def.Path != a || it.Def.Line != 7 || it.Owner != "Point" || it.Kind != "func" {
		t.Errorf("Lookup(Point.Sum) = %+v, %v", it, ok)
	}
	std, ok := s.Lookup("str.Len")
	if !ok || !strings.HasPrefix(std.Def.Path, "std/") || std.Owner != "str" || std.Name != "Len" || !std.Def.IsValid() {
		t.Errorf("Lookup(str.Len) = %+v, %v", std, ok)
	} else if f, ok := res.Sources.ByPath(std.Def.Path); !ok || !strings.HasPrefix(f.Line(int(std.Def.Line))[std.Def.Col-1:], "Len") {
		t.Errorf("Lookup(str.Len) at %v does not name Len", std.Def)
	}
	if _, ok := s.Lookup("NoSuchThing"); ok {
		t.Error("Lookup(NoSuchThing) found something")
	}

	// UsesOf: across files, the name's segment, never the declaration.
	pos := func(ps []source.Position) []string {
		var out []string
		for _, p := range ps {
			out = append(out, filepath.Base(p.Path)+":"+itoaQ(int(p.Line))+":"+itoaQ(int(p.Col)))
		}
		return out
	}
	if syntax {
		if got := pos(s.UsesOf("Point.Sum")); !slices.Equal(got, []string{"b.mk:4:12"}) {
			t.Errorf("UsesOf(Point.Sum) = %q", got)
		}
		if got := pos(s.UsesOf("Point.New")); !slices.Equal(got, []string{"b.mk:2:16"}) {
			t.Errorf("UsesOf(Point.New) = %q", got)
		}
		// Point: Point.Sum's and Point.New's owners, New's result, the
		// construction, b.mk's Point.New.
		if got := pos(s.UsesOf("Point")); !slices.Equal(got, []string{"a.mk:7:8", "a.mk:11:8", "a.mk:11:35", "a.mk:12:12", "b.mk:2:10"}) {
			t.Errorf("UsesOf(Point) = %q", got)
		}
		if got := pos(s.UsesOf("str.Len")); !slices.Equal(got, []string{"b.mk:6:12"}) {
			t.Errorf("UsesOf(str.Len) = %q", got)
		}
	}

	// TypeAt: inferred locals, a use, a call, a field.
	for _, c := range []struct{ anchor, what, want string }{
		{"p :=", "p", "Point"},
		{"q := p", "q", "Point"},
		{"q := p", "p", "Point"},
		{"n := q", "q", "Point"},
		{"n := q", "n", "int"},
		{"n := q", "q.Sum()", "int"},
		{"k := s", "k", "int"},
		{"s :=", "s", "str"},
		{"y := x", "x", "int"}, // in a fn literal: its own unit
		{"return y", "y", "int"},
		{"return f", "f", "fn(int): int"},
	} {
		off := offAfter(t, res, b, c.anchor, c.what)
		got, ok := s.TypeAt(b, off)
		if !syntax {
			if ok {
				t.Errorf("TypeAt after %q in a cache-hit module = %q, want false", c.what, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("TypeAt(%q in %q) = %q, %v; want %q", c.what, c.anchor, got, ok, c.want)
		}
	}
	if syntax {
		if got, ok := s.TypeAt(a, offAfter(t, res, a, "return self.x", "self.x")); !ok || got != "int" {
			t.Errorf("TypeAt(self.x) = %q, %v", got, ok)
		}
		if got, ok := s.TypeAt(a, offAfter(t, res, a, "return self", "self")); !ok || got != "&Point" {
			t.Errorf("TypeAt(self) = %q, %v", got, ok)
		}
		if _, ok := s.TypeAt(a, 0); ok {
			t.Error("TypeAt outside every body answered")
		}
	}

	// QueryChecked: Query's answers without Query's check.
	for _, q := range [][]string{{"sig", "Point.Sum"}, {"members", "Point"}, {"kind", "Point"}, {"sig", "str.Len"}, {"find", "len"}} {
		ans, err := s.QueryChecked(q[0], q[1:])
		if err != nil || ans == nil || !ans.Found || ans.Partial {
			t.Errorf("QueryChecked %v = %+v, %v", q, ans, err)
		}
	}
	if _, err := s.QueryChecked("nosuch", nil); err == nil {
		t.Error("QueryChecked of an unknown query: no error")
	}
}

// TestQueryChecked_SameAsQuery: every query answers the same through
// Query (its own Check) and through QueryChecked over a session that
// checked once, so the two dispatches cannot drift.
func TestQueryChecked_SameAsQuery(t *testing.T) {
	root := writeTree(t, lspFiles)
	cache := t.TempDir()
	s, err := NewSession(Config{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueryChecked("modules", nil); err == nil {
		t.Error("QueryChecked before any Check: no error")
	}
	if res := s.Check(); len(res.Report.Diags) > 0 {
		t.Fatalf("check:\n%s", res.Report.Text(res.Sources))
	}
	args := map[string][]string{"why-live": {"Point.New", "12"}, "modules": nil, "find": {"len"}}
	for _, d := range QueryNames {
		for _, subj := range []string{"Point", "Main", "Point.New", "Point.Sum", "str.Len", "Shape", "nosuch"} {
			a := args[d.Name]
			if a == nil && d.Args == 1 {
				a = []string{subj}
			}
			got, err := s.QueryChecked(d.Name, a)
			if err != nil {
				t.Fatal(err)
			}
			q, err := NewSession(Config{Root: root, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			want, _, err := q.Query(d.Name, a)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.JSON()) != string(want.JSON()) {
				t.Errorf("%s %v:\nQueryChecked %s\nQuery        %s", d.Name, a, got.JSON(), want.JSON())
			}
			if d.Args != 1 || args[d.Name] != nil {
				break
			}
		}
	}
}

// TestQueryChecked_Partial: a package that stops before types answers the
// same through both, Partial included; a parse error answers nil in both.
func TestQueryChecked_Partial(t *testing.T) {
	for _, c := range []struct {
		name, text string
		nilAns     bool
	}{
		{"resolve", "export Main(sys: &System) {\n    _ = Missing(1)\n}\n", false},
		{"parse", "export Main(sys: &System) {\n    _ = (\n}\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"m.mk": c.text})
			cache := t.TempDir()
			s, err := NewSession(Config{Root: root, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			if res := s.Check(); len(res.Report.Diags) == 0 {
				t.Fatal("the package checks")
			}
			got, err := s.QueryChecked("sig", []string{"str.Len"})
			if err != nil {
				t.Fatal(err)
			}
			q, err := NewSession(Config{Root: root, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			want, _, err := q.Query("sig", []string{"str.Len"})
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (want == nil) || (got == nil) != c.nilAns {
				t.Fatalf("QueryChecked %v, Query %v; want nil %v", got, want, c.nilAns)
			}
			if got != nil && (!got.Partial || string(got.JSON()) != string(want.JSON())) {
				t.Errorf("QueryChecked %sQuery        %s", got.JSON(), want.JSON())
			}
		})
	}
}
