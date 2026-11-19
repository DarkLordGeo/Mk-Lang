// Why: the untrusted-input and cancel-safety lists in safety.go are only useful
// while every name in them is a real test and every marked test is in them.
// What: tests of judgeUntrusted, judgeCancel and mathStep, and scans that tie
// each list to the Test and Fuzz functions of std's (and internal/git's) tests.
// How: parses the test files with go/parser (stdTestDecls) and compares names
// both ways; marked tests carry a marker string in their doc or file header.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stdTestFuncs are the names of the top-level functions of std's Go test
// files.
func stdTestFuncs(t *testing.T) map[string]bool {
	t.Helper()
	return pkgTestFuncs(t, "./std")
}

// pkgTestFuncs are the names of the top-level Test and Fuzz functions of
// the Go test files of pkg, a module-relative package path such as
// "./internal/git".
func pkgTestFuncs(t *testing.T, pkg string) map[string]bool {
	t.Helper()
	dir := filepath.Join(append([]string{"..", ".."}, strings.Split(strings.TrimPrefix(pkg, "./"), "/")...)...)
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("%s's test files: %v (%d)", pkg, err, len(files))
	}
	fn := regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]*)\(`)
	out := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range fn.FindAllSubmatch(b, -1) {
			out[string(m[1])] = true
		}
	}
	return out
}

// TestListedTargetsExist: every fuzz target of the untrusted list is a Fuzz
// function in its package's tests (std's, or the package untrustedTargetPkg
// names), so a rename fails here before a gate.
func TestListedTargetsExist(t *testing.T) {
	funcs := map[string]map[string]bool{}
	seen := map[string]bool{}
	for _, e := range untrusted {
		for _, n := range e.targets {
			p := untrustedPkgOf(n)
			if funcs[p] == nil {
				funcs[p] = pkgTestFuncs(t, p)
			}
			if !funcs[p][n] || !strings.HasPrefix(n, "Fuzz") {
				t.Errorf("%s: %s is no Fuzz function of %s's tests", e.entry, n, p)
			}
			if seen[n] {
				t.Errorf("%s listed twice", n)
			}
			seen[n] = true
		}
	}
}

// TestListedTestsExist: every test of the cancel-safety list is a Test
// function of its package's tests.
func TestListedTestsExist(t *testing.T) {
	fs := stdTestFuncs(t)
	for _, c := range cancelSafety {
		if c.pkg != "./std" || !fs[c.test] || !strings.HasPrefix(c.test, "Test") {
			t.Errorf("%s %s is no Test function of std's tests", c.pkg, c.test)
		}
	}
}

// TestUntrustedJudgement: a missing target fails every tier; an owed
// entry and an unrun target are notes in the quick tier and failures in
// the full one; a failed target fails.
func TestUntrustedJudgement(t *testing.T) {
	var found []string
	passed := map[string]bool{}
	for _, e := range untrusted {
		for _, n := range e.targets {
			found = append(found, untrustedPkgOf(n)+" "+n)
			passed[untrustedPkgOf(n)+" "+n] = true
		}
	}
	owed := 0
	for _, e := range untrusted {
		if len(e.targets) == 0 {
			owed++
		}
	}
	s := &Step{Status: "pass"}
	judgeUntrusted(s, "quick", found, passed)
	if s.Status != "pass" {
		t.Errorf("quick, everything ran: %s %s", s.Status, s.Reason)
	}
	s = &Step{Status: "pass"}
	judgeUntrusted(s, "full", found, passed)
	if owed > 0 && (s.Status != "fail" || len(s.Failed) != owed) {
		t.Errorf("full with %d owed entries: %s, failed %q", owed, s.Status, s.Failed)
	}
	first := untrustedPkgOf(untrusted[0].targets[0]) + " " + untrusted[0].targets[0]
	delete(passed, first)
	s = &Step{Status: "pass"}
	judgeUntrusted(s, "quick", found, passed)
	if s.Status != "pass" || !strings.Contains(strings.Join(s.Notes, "\n"), "not run in this tier") {
		t.Errorf("quick, one target unselected: %s %q", s.Status, s.Notes)
	}
	passed[first] = false
	s = &Step{Status: "pass"}
	judgeUntrusted(s, "quick", found, passed)
	if s.Status != "fail" || s.Failed[0] != first {
		t.Errorf("a failed target: %s %q", s.Status, s.Failed)
	}
	passed[first] = true
	s = &Step{Status: "pass"}
	judgeUntrusted(s, "quick", found[1:], passed)
	if s.Status != "fail" || !strings.Contains(s.Reason, "is not a fuzz target") {
		t.Errorf("a missing target: %s %s", s.Status, s.Reason)
	}
}

// TestCancelJudgement: each listed test must pass; a Linux leaf that
// only skipped is a note in the quick tier and a failure in the full one;
// an absent test fails; -only notes the packages it leaves out.
func TestCancelJudgement(t *testing.T) {
	all := func(string) bool { return true }
	res := map[string]string{}
	for _, c := range cancelSafety {
		res[c.pkg+" "+c.test] = "pass"
	}
	s := &Step{Status: "pass"}
	judgeCancel(s, true, all, res, nil)
	if s.Status != "pass" {
		t.Fatalf("all passed: %s %s", s.Status, s.Reason)
	}
	var linux, plain string
	for _, c := range cancelSafety {
		if c.linux && linux == "" {
			linux = c.pkg + " " + c.test
		}
		if !c.linux && plain == "" {
			plain = c.pkg + " " + c.test
		}
	}
	res[linux] = "skip"
	s = &Step{Status: "pass"}
	judgeCancel(s, false, all, res, nil)
	if s.Status != "pass" || len(s.Notes) != 1 {
		t.Errorf("quick, Linux leaf skipped: %s %q", s.Status, s.Notes)
	}
	s = &Step{Status: "pass"}
	judgeCancel(s, true, all, res, nil)
	if s.Status != "fail" || s.Failed[0] != linux {
		t.Errorf("full, Linux leaf skipped: %s %q", s.Status, s.Failed)
	}
	res[linux] = "pass"
	res[plain] = "skip"
	s = &Step{Status: "pass"}
	judgeCancel(s, false, all, res, nil)
	if s.Status != "fail" {
		t.Errorf("a Windows test skipped: %s", s.Status)
	}
	delete(res, plain)
	s = &Step{Status: "pass"}
	judgeCancel(s, false, all, res, nil)
	if s.Status != "fail" || !strings.Contains(s.Reason, "did not run") {
		t.Errorf("an absent test: %s %s", s.Status, s.Reason)
	}
	s = &Step{Status: "pass"}
	judgeCancel(s, true, func(string) bool { return false }, map[string]string{}, nil)
	if s.Status != "pass" || len(s.Notes) != len(cancelSafety) {
		t.Errorf("-only without ./std: %s %d notes", s.Status, len(s.Notes))
	}
}

// TestParseRecordsTopLevelResults: go test -json's top-level results
// are kept by package and name, the last one winning; subtests are not.
func TestParseRecordsTopLevelResults(t *testing.T) {
	tp := newTestParse()
	for _, l := range []string{
		`{"Action":"run","Package":"mk/std","Test":"TestA"}`,
		`{"Action":"run","Package":"mk/std","Test":"TestA/sub"}`,
		`{"Action":"pass","Package":"mk/std","Test":"TestA/sub"}`,
		`{"Action":"pass","Package":"mk/std","Test":"TestA"}`,
		`{"Action":"skip","Package":"mk/std","Test":"TestB"}`,
		`{"Action":"pass","Package":"mk/std"}`,
	} {
		tp.line(l)
	}
	if tp.results["./std TestA"] != "pass" || tp.results["./std TestB"] != "skip" || len(tp.results) != 2 {
		t.Errorf("results %v", tp.results)
	}
}

// TestMathStep: the math checksum tests exist in std's tests, and the
// quick tier and -skip test leave the step out without running anything.
func TestMathStep(t *testing.T) {
	fs := stdTestFuncs(t)
	for _, m := range mathTests {
		if !fs[m.test] {
			t.Errorf("%s is no Test function of std's tests", m.test)
		}
	}
	for _, tc := range []struct {
		tier string
		skip map[string]bool
	}{{"quick", map[string]bool{}}, {"full", map[string]bool{"test": true}}} {
		g := &gate{tier: tc.tier, skip: tc.skip, rep: &Report{}}
		g.mathStep()
		if len(g.rep.Steps) != 1 || g.rep.Steps[0].Status != "skip" {
			t.Errorf("%s %v: %+v", tc.tier, tc.skip, g.rep.Steps)
		}
	}
}

// stdTestDecls are std's top-level Test and Fuzz functions, each with
// whether its doc comment names mark, and whether its file's header (the
// comments before the package clause) or, with whole, the file's text
// names it.
func stdTestDecls(t *testing.T, mark string, whole bool) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "std", "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("std's test files: %v (%d)", err, len(files))
	}
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, b, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		inFile := whole && strings.Contains(string(b), mark)
		for _, c := range file.Comments {
			if c.Pos() < file.Package && strings.Contains(c.Text(), mark) {
				inFile = true
			}
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			n := fn.Name.Name
			if !strings.HasPrefix(n, "Test") && !strings.HasPrefix(n, "Fuzz") || n == "TestMain" {
				continue
			}
			out[n] = inFile || fn.Doc != nil && strings.Contains(fn.Doc.Text(), mark)
		}
	}
	return out
}

// TestFuzzTargetsListed: every Fuzz function of a std test file
// that holds the untrusted-input marker stands for a row of untrusted, or
// is exempt with a reason, so a new untrusted-input target cannot sit
// unjudged while its row says it is owed; an exempt name is no listed
// target and still carries the marker.
func TestFuzzTargetsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, e := range untrusted {
		for _, n := range e.targets {
			listed[n] = true
		}
	}
	decls := stdTestDecls(t, "untrusted-input", true)
	for n, marked := range decls {
		if !marked || !strings.HasPrefix(n, "Fuzz") {
			continue
		}
		if _, ok := untrustedFuzzExempt[n]; !listed[n] && !ok {
			t.Errorf("%s is an untrusted-input fuzz target of std's tests in no row of untrusted (tools/gate/safety.go) and not exempt", n)
		}
	}
	for n := range untrustedFuzzExempt {
		if _, ok := decls[n]; listed[n] || !ok {
			t.Errorf("exempt %s: listed, or no longer a fuzz target of std's tests", n)
		}
	}
}

// TestCancelSafetyTestsListed: every top-level test of std whose doc comment
// or file header holds the cancel-safety marker is a row of cancelSafety,
// or exempt with a reason, so a cancel-safety test that is skipped, renamed or
// removed cannot leave the cancel-safety step reporting every test passed.
func TestCancelSafetyTestsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range cancelSafety {
		if listed[c.test] {
			t.Errorf("%s listed twice", c.test)
		}
		listed[c.test] = true
	}
	decls := stdTestDecls(t, "cancel-safety", false)
	for n, marked := range decls {
		if !marked || !strings.HasPrefix(n, "Test") {
			continue
		}
		if _, ok := cancelExempt[n]; !listed[n] && !ok {
			t.Errorf("%s is marked cancel-safety (its doc or its file's header) and is in no row of cancelSafety (tools/gate/safety.go) and not exempt", n)
		}
	}
	for n := range cancelExempt {
		if _, ok := decls[n]; listed[n] || !ok {
			t.Errorf("exempt %s: listed, or no longer a test of std", n)
		}
	}
	for _, n := range []string{"TestRt_NotifyWaitSim", "TestStdNet_SimSweep"} {
		if !listed[n] {
			t.Errorf("%s, the Notify.Wait and Conn.Close cancel-safety test, not listed", n)
		}
	}
}
