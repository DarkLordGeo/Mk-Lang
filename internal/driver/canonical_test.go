// Why: mk fix --canonical rewrites code into the canonical style, so each
// rewrite must keep the program's meaning, converge, and agree with mk fmt.
// What: tests of the value-arm, for-in and else-default rounds, std as the
// subject (mk fix --canonical --std=<module>), error formatting, kept
// comments, and TestCanonicalIdempotentOnTree over every clean example,
// program and package in the repository.
// How: runs the rounds on sample sources, recompiles each rewrite, and runs
// mk fix twice to check the second run changes nothing.

package driver

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/format"
)

// canonicalFix runs mk fix --canonical on src as one file and returns the
// rewritten text and the rounds; a second run must change nothing and the
// text must be a fixed point of mk fmt.
func canonicalFix(t *testing.T, src string) (string, []FixRound) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() *FixResult {
		s, err := NewSession(Config{Root: file, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{Canonical: true})
		if err != nil || res.Stopped != "clean" || len(res.Final.Report.Diags) != 0 {
			t.Fatalf("mk fix --canonical: %v %q", err, res.Stopped)
		}
		return res
	}
	res := run()
	got, _ := os.ReadFile(file)
	if again := run(); len(again.Rounds) != 0 {
		t.Errorf("second run rewrote again: %+v\n%s", again.Rounds, got)
	}
	if out, err := format.Source("main.mk", got); err != nil || string(out) != string(got) {
		t.Errorf("not a fixed point of mk fmt (%v):\n%s", err, got)
	}
	return string(got), res.Rounds
}

func wantLines(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
}

func TestCanonicalValueArm(t *testing.T) {
	t.Parallel()
	src := `enum Shape {
    Circle(r: f64)
    Rect(w: f64, h: f64)
    Empty
}

Name(s: Shape): str {
    var kind: str
    match s {
        .Circle {
            kind = "circle"
        }
        .Rect(w, h) {
            _ = w + h
            kind = "rect"
        }
        .Empty {
            return "nothing"
        }
    }
    return kind
}

Sign(n: int): i8 {
    var v: i8
    if n < 0 {
        v = -1
    } else if n == 0 {
        v = 0
    } else {
        v = 1
    }
    return v
}

Later(n: int): i8 {
    var v: i8
    if n < 0 {
        v = -1
    } else {
        v = 1
    }
    v += 1
    return v
}

Kept(n: int): int {
    var v: int
    if n < 0 {
        v = 1
    }
    v = 2
    var w: int
    if n < 0 {
        w = 1
    } else {
        w = 2
        w = w + 2
    }
    return v + w
}
`
	got, rounds := canonicalFix(t, src)
	if len(rounds) != 1 || rounds[0].Phase != "value-arm" || len(rounds[0].Applied) != 2 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	wantLines(t, got,
		"    kind: str = match s {\n        .Circle {\n            \"circle\"\n        }\n",
		"            _ = w + h\n            \"rect\"\n",
		"    v: i8 = if n < 0 {\n        -1\n    } else if n == 0 {\n        0\n    } else {\n        1\n    }\n    return v\n",
		// assigned after the construct ("nothing else assigns it"): left
		"    var v: i8\n    if n < 0 {\n        v = -1\n    } else {\n        v = 1\n    }\n    v += 1\n",
		// no else: left; w read in an arm: left
		"    var v: int\n    if n < 0 {\n        v = 1\n    }\n",
		"        w = w + 2\n")
}

func TestCanonicalForIn(t *testing.T) {
	t.Parallel()
	src := `struct Count {
    n: int
}

Count.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

Sum(k: int): int {
    var total := 0
    var it := Count(n: k)
    for x := it.Next() {
        total += x
    }
    var it2 := Count(n: k)
    for x := it2.Next() {
        total += x
    }
    _ = it2.Next()
    return total
}
`
	got, rounds := canonicalFix(t, src)
	if len(rounds) != 1 || rounds[0].Phase != "for-in" || len(rounds[0].Applied) != 1 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	wantLines(t, got, "    var total := 0\n    for x in Count(n: k) {\n        total += x\n    }\n    var it2 := Count(n: k)\n    for x := it2.Next() {\n")
}

// TestCanonicalElseDefaultOperand: an Or that is an operand or an argument's
// part is parenthesised; a return's is not. The rewrite is E0207's proven
// fix, applied by mk fix's ordinary rounds.
func TestCanonicalElseDefaultOperand(t *testing.T) {
	t.Parallel()
	src := `Find(xs: []int, k: int): ?int {
    for x in xs {
        if x == k {
            return some(x)
        }
    }
    return none
}

Pick(xs: []int): int {
    Assert(Find(xs, 1).Or(-1) == -1)
    n := Find(xs, 2).Or(0) + 1
    return Find(xs, 3).Or(n)
}
`
	got, rounds := canonicalFix(t, src)
	applied := 0
	for _, r := range rounds {
		applied += len(r.Applied)
	}
	if applied != 3 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	wantLines(t, got, "    Assert((Find(xs, 1) else { -1 }) == -1)\n", "    n := (Find(xs, 2) else { 0 }) + 1\n", "    return Find(xs, 3) else { n }\n")
}

// TestCanonicalStdSubject: mk fix --canonical --std=std.hash rewrites std's own
// files under MK_STD, touches no other module, and a second run changes
// nothing.
func TestCanonicalStdSubject(t *testing.T) {
	t.Parallel()
	const hashFile = "Pickt12(o: ?int): int {\n    return o.Or(0)\n}\n"
	const textFile = "Pickt12(o: ?int): int {\n    return o else { 1 }\n}\n"
	dir := copyStd(t, map[string]string{"hash/t12.mk": hashFile, "text/t12.mk": textFile}, nil)
	cache := t.TempDir()
	run := func() *FixResult {
		s, err := NewSession(Config{StdTests: "std.hash", StdDir: dir, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{Canonical: true})
		if err != nil || res.Stopped != "clean" {
			t.Fatalf("mk fix --canonical --std=std.hash: %v %q", err, res.Stopped)
		}
		return res
	}
	// Warm the cache first: the subject must load whole even when every
	// std module is a module-cache hit (release.go loadsWhole).
	warm, err := NewSession(Config{StdTests: "std.hash", StdDir: dir, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if r := warm.Check(); len(r.Report.Diags) != 1 {
		t.Fatalf("std copy: want the one E0207 of hash/t12.mk:\n%s", diagLines(r))
	}
	res := run()
	if len(res.Files) != 1 || filepath.Base(res.Files[0]) != "t12.mk" || !strings.Contains(filepath.ToSlash(res.Files[0]), "/hash/") {
		t.Fatalf("files %v rounds %+v", res.Files, res.Rounds)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "hash", "t12.mk"))
	if !strings.Contains(string(got), "return o else { 0 }\n") {
		t.Errorf("std/hash/t12.mk:\n%s", got)
	}
	if other, _ := os.ReadFile(filepath.Join(dir, "text", "t12.mk")); string(other) != textFile {
		t.Errorf("std/text/t12.mk changed:\n%s", other)
	}
	if again := run(); len(again.Rounds) != 0 {
		t.Errorf("second run: %+v", again.Rounds)
	}
	// The embedded std is never the subject.
	s, err := NewSession(Config{StdTests: "std.hash", CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fix(FixOptions{Canonical: true}); err == nil {
		t.Errorf("mk fix --std without MK_STD: no usage error")
	}
}

func TestCanonicalErrorFormat(t *testing.T) {
	t.Parallel()
	src := `error NotFound(id: i64, code: u8)
error Denied

Show(w: mut Writer, e: NotFound | Denied): WriteError!void {
    match e {
        NotFound(code, id) {
            try Print(w, "error: NotFound(id: {id}, code: {code})\n", (id, code))
        }
        Denied {
            try Print(w, "error: Denied\n")
        }
    }
}

Other(w: mut Writer, e: NotFound | Denied): WriteError!void {
    match e {
        NotFound(id, code) {
            try Print(w, "missing {code}\n", (code))
            _ = id
        }
        Denied {
            try Print(w, "error: Denied\n")
        }
    }
}
`
	got, rounds := canonicalFix(t, src)
	if len(rounds) != 1 || rounds[0].Phase != "error-format" || len(rounds[0].Applied) != 1 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	wantLines(t, got, "    try Print(w, \"error: {e}\\n\", (e))\n}\n", "            try Print(w, \"missing {code}\\n\", (code))\n")
}

// TestCanonicalIdempotentOnTree runs over the documentation's example blocks,
// tests/programs and Dogfoods: on a copy of every `ok` block, every
// single-file program and every package (a directory with a pkg.mk, copied
// with its top directory so path deps resolve) that checks clean, mk fix
// --canonical runs, and a second run with the same cache changes nothing. A
// first run that rewrites is logged: the tree is meant to be a fixed point
// of the canonical style. std is covered by TestCanonicalIdempotentOnStd (the
// whole std, both targets) and TestCanonicalStdSubject (one module as the
// subject).
func TestCanonicalIdempotentOnTree(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: every design block and program is fixed twice")
	}
	t.Parallel()
	// A unit is one file's text, or a package: top (a directory of the
	// tree, copied whole) and rel (the package directory under it).
	type unit struct{ name, text, top, rel string }
	var units []unit
	for _, b := range designBlocks(t) {
		if b.verdict == "ok" {
			units = append(units, unit{name: b.name, text: b.text})
		}
	}
	pkgDirs := map[string]bool{}
	for _, root := range []string{"../../tests/programs", "../../Dogfoods"} {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "pkg.mk" {
				pkgDirs[filepath.ToSlash(filepath.Dir(p))] = true
			}
			return nil
		})
	}
	inPkg := func(p string) bool {
		for d := path.Dir(filepath.ToSlash(p)); strings.HasPrefix(d, "../../"); d = path.Dir(d) {
			if pkgDirs[d] {
				return true
			}
		}
		return false
	}
	progs, _ := filepath.Glob("../../tests/programs/*.mk")
	more, _ := filepath.Glob("../../tests/programs/*/*.mk")
	for _, p := range append(progs, more...) {
		if inPkg(p) {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, unit{name: strings.TrimPrefix(filepath.ToSlash(p), "../../"), text: string(raw)})
	}
	for d := range pkgDirs {
		rel := strings.TrimPrefix(d, "../../")
		parts := strings.Split(rel, "/")
		top := "../../" + parts[0] + "/" + parts[1] // tests/programs/<dir> or Dogfoods/<name>
		if parts[0] == "tests" {
			top += "/" + parts[2]
		}
		inner, err := filepath.Rel(top, d)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, unit{name: rel, top: top, rel: inner})
	}
	slices.SortFunc(units, func(a, b unit) int { return strings.Compare(a.name, b.name) })
	cache := t.TempDir()
	var mu sync.Mutex
	checked, rewritten, pkgs := 0, 0, 0
	t.Run("units", func(t *testing.T) {
		for _, u := range units {
			t.Run(strings.NewReplacer("/", "_", "#", "_").Replace(u.name), func(t *testing.T) {
				t.Parallel()
				file := filepath.Join(t.TempDir(), "main.mk")
				if u.top != "" {
					tmp := t.TempDir()
					if err := copyTreeNoCache(u.top, tmp); err != nil {
						t.Fatal(err)
					}
					file = filepath.Join(tmp, u.rel)
				} else if err := os.WriteFile(file, []byte(u.text), 0o644); err != nil {
					t.Fatal(err)
				}
				fix := func() *FixResult {
					s, err := NewSession(Config{Root: file, CacheDir: cache})
					if err != nil {
						t.Fatal(err)
					}
					res, err := s.Fix(FixOptions{Canonical: true})
					if err != nil {
						t.Fatal(err)
					}
					return res
				}
				s, err := NewSession(Config{Root: file, CacheDir: cache})
				if err != nil {
					t.Fatal(err)
				}
				if r := s.Check(); len(r.Report.Diags) > 0 {
					if u.top != "" {
						t.Logf("package %s skipped: it does not check clean here: %s", u.name, r.Report.Diags[0].Diag.Message)
					}
					return // needs the test prelude, another module or a target: not a unit here
				}
				first := fix()
				if len(first.Rounds) > 0 {
					t.Logf("%s is not a fixed point: %+v", u.name, first.Rounds)
				}
				second := fix()
				if len(second.Rounds) != 0 || second.Stopped != "clean" {
					t.Errorf("%s: second run %q %+v", u.name, second.Stopped, second.Rounds)
				}
				mu.Lock()
				checked++
				if u.top != "" {
					pkgs++
				}
				if len(first.Rounds) > 0 {
					rewritten++
				}
				mu.Unlock()
			})
		}
	})
	t.Logf("%d units checked clean and fixed twice (%d packages), %d rewritten by the first run", checked, pkgs, rewritten)
	if pkgs < 2 {
		t.Errorf("only %d packages checked clean: the package walk lost its subject", pkgs)
	}
	if checked < 50 {
		t.Errorf("only %d units checked clean: the harness lost its subject", checked)
	}
}

// copyTreeNoCache copies the tree at src into dst, skipping every
// .mk-cache directory: a package's sources are the unit, and a dogfood's
// caches (Dogfoods/notes and its repro packages hold gigabytes) copied once
// per unit would fill the disk.
func copyTreeNoCache(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			if d.Name() == ".mk-cache" {
				return filepath.SkipDir
			}
			return os.MkdirAll(to, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
}

// TestCanonicalOnlyThatLocal: the value-arm round rewrites a construct only
// when every arm assigns that one local; arms that also assign another
// outer local stay a statement, as the memory model's Late example does. A
// local declared inside an arm may be assigned there.
func TestCanonicalOnlyThatLocal(t *testing.T) {
	t.Parallel()
	src := `Late(c: bool): i32 {
    x: i32
    y: i32
    if c {
        x = 1
        y = 10
    } else {
        x = 2
        y = 20
    }
    return x + y
}

Inner(c: bool): int {
    x: int
    if c {
        var t := 1
        t += 1
        x = t
    } else {
        x = 2
    }
    return x
}
`
	got, rounds := canonicalFix(t, src)
	if len(rounds) != 1 || len(rounds[0].Applied) != 1 {
		t.Fatalf("rounds %+v\n%s", rounds, got)
	}
	wantLines(t, got, "    x: i32\n    y: i32\n    if c {\n        x = 1\n        y = 10\n", "    x: int = if c {\n        var t := 1\n        t += 1\n        t\n")
}

// TestCanonicalCommentsKept: a round never deletes a comment. A comment between
// the declaration and the construct (value-arm), between `var it` and the
// loop (for-in), or inside the match (error-format) leaves the code as
// written; for-in also leaves a place, which `for in` would iterate in
// place.
func TestCanonicalCommentsKept(t *testing.T) {
	t.Parallel()
	src := `error NotFound(id: i64)
error Denied

struct Count {
    n: int
}

Count.Next(mut self): ?int {
    if self.n == 0 {
        return none
    }
    self.n -= 1
    return some(self.n)
}

Pick(c: bool): int {
    x: int
    // chosen below
    if c {
        x = 1
    } else {
        x = 2
    }
    return x
}

Sum(k: int): int {
    var total := 0
    var it := Count(n: k)
    // counts down
    for x := it.Next() {
        total += x
    }
    var saved := Count(n: k)
    var it2 := saved
    for x := it2.Next() {
        total += x
    }
    _ = saved.Next()
    return total
}

Show(w: mut Writer, e: NotFound | Denied): WriteError!void {
    match e {
        NotFound(id) {
            // the id is the key
            try Print(w, "NotFound(id: {id})", (id))
        }
        Denied {
            try Print(w, "Denied")
        }
    }
}
`
	got, rounds := canonicalFix(t, src)
	if got != src {
		t.Errorf("rounds %+v rewrote:\n%s", rounds, got)
	}
}
