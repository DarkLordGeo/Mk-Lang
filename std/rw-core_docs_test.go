// Why: std.core's docs are what users read in hover and mk doc; they must
// state costs and compile errors, and the code must follow the house style.
// What: TestRwCore_SectionEighteenDocsGiveCost, MapIterDocsGiveSparseCost,
// DropBoundDocsNameE0701, NoDeadInitializerBeforeTwoArms,
// StoreOfLinearIsE0701 and TestStdFilesWithinSizeRule.
// How: text scans of std's Mk sources and /// blocks; no program is built.

// Style tests over std/core and the std directories next to it: every
// public function that std.core declares gives its worst-case cost; a
// call the checker refuses for a
// linear type says so (E0701) instead of promising a panic; a map
// iterator's step states the cost of a sparse table; no local is declared
// with an initializer that both arms of the following if overwrite; and no
// std Mk file is longer than archtest's file-size rule.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// docAbove returns the /// block directly above line i of lines, joined by
// spaces, with the slashes dropped.
func docAbove(lines []string, i int) string {
	j := i
	for j > 0 && strings.HasPrefix(lines[j-1], "///") {
		j--
	}
	var b []string
	for _, l := range lines[j:i] {
		b = append(b, strings.TrimSpace(strings.TrimPrefix(l, "///")))
	}
	return strings.Join(b, " ")
}

// coreExport is one top-level export line of a std/core library file.
type coreExport struct {
	file       string
	line       int
	recv, name string
	doc        string
}

// coreExports reads every export of std/core's library files (tests_*.mk
// hold tests).
func coreExports(t *testing.T) []coreExport {
	t.Helper()
	files, err := filepath.Glob("core/*.mk")
	if err != nil || len(files) == 0 {
		t.Fatalf("no std/core files: %v", err)
	}
	var out []coreExport
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "tests_") {
			continue
		}
		lines := strings.Split(readText(t, filepath.ToSlash(f)), "\n")
		for i, l := range lines {
			if !strings.HasPrefix(l, "export ") {
				continue
			}
			recv, name := itemHead(strings.TrimPrefix(l, "export "))
			out = append(out, coreExport{file: filepath.ToSlash(f), line: i + 1, recv: recv, name: name, doc: docAbove(lines, i)})
		}
	}
	return out
}

// TestRwCore_SectionEighteenDocsGiveCost: each public function or
// method of the standard library that std.core declares has a /// block
// that states its worst-case cost as O(...).
func TestRwCore_SectionEighteenDocsGiveCost(t *testing.T) {
	exports := coreExports(t)
	checked := 0
	for _, it := range parseReferenceAPI(t) {
		if it.mod != "std.core" || it.block || !strings.Contains(it.text, "(") {
			continue
		}
		if strings.HasPrefix(it.text, "error ") || strings.HasPrefix(it.text, "type ") || strings.HasPrefix(it.text, "const ") {
			continue
		}
		recv, name := itemHead(it.text)
		for _, e := range exports {
			if e.recv != recv || e.name != name {
				continue
			}
			checked++
			if !strings.Contains(e.doc, "O(") {
				t.Errorf("%s:%d: %s's /// block states no worst-case cost (J3): %q", e.file, e.line, it.text, e.doc)
			}
		}
	}
	t.Logf("%d library-surface functions of std.core checked", checked)
	if checked < 100 {
		t.Fatalf("matched only %d library-surface functions in std/core; the parser or the layout changed", checked)
	}
}

// TestRwCore_MapIterDocsGiveSparseCost (RWCORE-4): a table never shrinks
// (TableErase only rewrites control bytes), so one Next on a map that
// removals left sparse reads up to capacity / 8 groups. The step's /// block
// says so and does not promise O(1) per entry.
func TestRwCore_MapIterDocsGiveSparseCost(t *testing.T) {
	for _, e := range coreExports(t) {
		if e.name != "Next" || (e.recv != "MapIter" && e.recv != "TextMapIter") {
			continue
		}
		if strings.Contains(e.doc, "O(1) per entry") || !strings.Contains(e.doc, "capacity") {
			t.Errorf("%s:%d: %s.Next's /// block must give the sparse-table cost in terms of capacity: %q", e.file, e.line, e.recv, e.doc)
		}
	}
}

// freeboundRow matches a std.core or std.atomic row of the checker's table
// of calls that would drop a linear value (internal/check/freebound.go).
var freeboundRow = regexp.MustCompile(`\{"(std\.core|std\.atomic)", "([A-Za-z]+)", "([A-Za-z]+)",`)

// TestRwCore_DropBoundDocsNameE0701: a call the checker
// refuses for a linear type is E0701 at the call, so its /// block
// names E0701 and never promises only a panic. std.atomic's
// SpinGuard.Store is checked through core.SpinStore, which holds its body.
func TestRwCore_DropBoundDocsNameE0701(t *testing.T) {
	src, err := os.ReadFile("../internal/check/freebound.go")
	if err != nil {
		t.Fatal(err)
	}
	rows := freeboundRow.FindAllStringSubmatch(string(src), -1)
	if len(rows) == 0 {
		t.Fatal("no std.core or std.atomic rows in internal/check/freebound.go")
	}
	exports := coreExports(t)
	for _, r := range rows {
		recv, name := r[2], r[3]
		if name == "Free" {
			continue // an impl of Free, documented on its impl block, not an export
		}
		if r[1] == "std.atomic" {
			if recv != "SpinGuard" || name != "Store" {
				continue // not held in std/core
			}
			recv, name = "", "SpinStore"
		}
		found := false
		for _, e := range exports {
			if e.recv != recv || e.name != name {
				continue
			}
			found = true
			if !strings.Contains(e.doc, "E0701") {
				t.Errorf("%s:%d: %s.%s is refused for a linear type at the call (freebound.go), and its /// block does not say E0701: %q", e.file, e.line, r[2], r[3], e.doc)
			}
		}
		if !found {
			t.Errorf("freebound.go row %s %s.%s: no export of it in std/core", r[1], r[2], r[3])
		}
	}
}

// deadInit matches `var x := e` (or `var x: T = e`), and twoArmed the
// `if c { x = a } else { x = b }` after it.
var deadInit = regexp.MustCompile(`^(\s*)var ([A-Za-z_][A-Za-z0-9_]*)(?:\s*:[^=]+)?\s*:?=\s*\S`)

// deadInitSites returns the 1-based lines of lines where a local is declared
// with an initializer that both arms of the next if overwrite, and that no
// later line of the function assigns or lends mutably.
func deadInitSites(lines []string) []int {
	var out []int
	for i := 0; i+5 < len(lines); i++ {
		m := deadInit.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ind, x := m[1], regexp.QuoteMeta(m[2])
		assign := regexp.MustCompile(`^\s*` + x + ` = `)
		if !strings.HasPrefix(lines[i+1], ind+"if ") || !strings.HasSuffix(lines[i+1], "{") ||
			!assign.MatchString(lines[i+2]) || strings.TrimSpace(lines[i+3]) != "} else {" ||
			!assign.MatchString(lines[i+4]) || strings.TrimSpace(lines[i+5]) != "}" {
			continue
		}
		again := regexp.MustCompile(`(^|[^A-Za-z0-9_.])` + x + `\s*([-+*/%&|^]|<<|>>)?=[^=]|mut ` + x + `\b`)
		reassigned := false
		for k := i + 6; k < len(lines) && !strings.HasPrefix(lines[k], "}"); k++ {
			if again.MatchString(lines[k]) {
				reassigned = true
				break
			}
		}
		if !reassigned {
			out = append(out, i+1)
		}
	}
	return out
}

// TestRwCore_NoDeadInitializerBeforeTwoArms: a
// local whose initializer both arms of the next if overwrite, and that
// nothing assigns again, is a value chosen by a branch: a value arm. mk fix
// --canonical's value-arm round does not yet see this shape, so this test
// holds it for std/core, raw, corpus, pkg, derive, dyn and libc. The
// first subtest pins the detector on
// the shapes it must and must not report.
func TestRwCore_NoDeadInitializerBeforeTwoArms(t *testing.T) {
	t.Run("detector", func(t *testing.T) {
		dead := "F(c: bool): int {\n    var d := 0\n    if c {\n        d = 1\n    } else {\n        d = 2\n    }\n    return d\n}\n"
		live := strings.Replace(dead, "    return d", "    d += 1\n    return d", 1)
		oneArm := "F(c: bool): int {\n    var d := 0\n    if c {\n        d = 1\n    }\n    return d\n}\n"
		if got := deadInitSites(strings.Split(dead, "\n")); len(got) != 1 || got[0] != 2 {
			t.Errorf("dead initializer: got %v, want [2]", got)
		}
		if got := deadInitSites(strings.Split(live, "\n")); len(got) != 0 {
			t.Errorf("reassigned later: got %v, want none", got)
		}
		if got := deadInitSites(strings.Split(oneArm, "\n")); len(got) != 0 {
			t.Errorf("one arm: got %v, want none", got)
		}
	})
	for _, dir := range []string{"core", "raw", "corpus", "pkg", "derive", "dyn", "libc"} {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
				return err
			}
			lines := strings.Split(strings.ReplaceAll(readText(t, filepath.ToSlash(p)), "\r\n", "\n"), "\n")
			for _, n := range deadInitSites(lines) {
				t.Errorf("%s:%d: this local is chosen by the if below and assigned nowhere else: write x := if ... { ... } else { ... }", filepath.ToSlash(p), n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// maxFileLines reads archtest's MaxFileLines (internal/archtest/dag.go) as
// text: package std imports no compiler package.
func maxFileLines(t *testing.T) int {
	t.Helper()
	src, err := os.ReadFile("../internal/archtest/dag.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const MaxFileLines = ([0-9]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("internal/archtest/dag.go declares no MaxFileLines")
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// TestStdFilesWithinSizeRule: no std Mk file is longer, in
// newlines, than archtest's MaxFileLines. A file a Go test generates whole
// (its first line "// Generated by" or "// Code generated by", as stdLines
// counts them) is a table, not a concept, and is exempt. One subtest per top-level std directory, plus "toplevel" for the
// Mk files directly under std/, as TestStdNoProcessResidue.
func TestStdFilesWithinSizeRule(t *testing.T) {
	t.Parallel()
	limit := maxFileLines(t)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, p string) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(string(b), "// Generated by") || strings.HasPrefix(string(b), "// Code generated by") {
			return
		}
		if n := strings.Count(string(b), "\n"); n > limit {
			t.Errorf("%s has %d lines, over archtest's %d (J10): split it by concept", filepath.ToSlash(p), n, limit)
		}
	}
	t.Run("toplevel", func(t *testing.T) {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mk") {
				check(t, e.Name())
			}
		}
	})
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := e.Name()
		t.Run(dir, func(t *testing.T) {
			t.Parallel()
			err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && p != dir) {
						return filepath.SkipDir
					}
					return nil
				}
				if strings.HasSuffix(p, ".mk") {
					check(t, p)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRwCore_StoreOfLinearIsE0701 (RWCORE-1): Guard.Store and
// SpinGuard.Store of a linear T are refused at the call with E0701 naming
// Replace, as their /// blocks say, so neither code 8 panic is reachable
// from a program. Check only: no native build.
func TestRwCore_StoreOfLinearIsE0701(t *testing.T) {
	dir := t.TempDir()
	src := "import std.atomic\n\nlinear struct Tok {\n    n: int\n}\n\n" +
		"PutSpin(g: mut atomic.SpinGuard[Tok], x: Tok) {\n    g.Store(x)\n}\n\n" +
		"PutGuard(g: mut Guard[Tok], x: Tok) {\n    g.Store(x)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	stdDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(dir, "cache"))
	out, status := mkCmd(t, dir, stdDir, "check", "main.mk")
	if status == 0 {
		t.Fatalf("mk check accepted a Store of a linear value:\n%s", out)
	}
	for _, want := range []string{
		"main.mk:8:7: E0701 bound-unmet: `Tok` is linear: `SpinGuard.Store` would drop it",
		"main.mk:12:7: E0701 bound-unmet: `Tok` is linear: `Guard.Store` would drop it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mk check output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "try: Replace(g.Mut(), x)") != 2 {
		t.Errorf("each E0701 should name Replace(g.Mut(), x):\n%s", out)
	}
}
