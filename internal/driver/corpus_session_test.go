// Why: mk corpus checks thousands of units against one std, and how far
// each is judged depends on what that std provides.
// What: tests of the corpus seam: one std per session, one module per
// unit, Through by std's state, Paths, directory units and StdState.
// How: builds stand-in std trees in temp directories (m1Std and others)
// and checks units through CheckBlock and CheckUnit.

package driver

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/diag"
)

// m1Std writes an empty std: a directory holding no .mk file, so it forms
// no module and declares no prelude name.
func m1Std(t testing.TB) string {
	return writeTree(t, map[string]string{"README.md": "# std\n"})
}

func TestThroughLoweredByStd(t *testing.T) {
	// A std with no std.core (an empty std): units are judged through parse.
	c, err := NewCorpusSession(Config{StdDir: m1Std(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Through() != diag.PhaseParse {
		t.Errorf("empty std: Through %v, want parse", c.Through())
	}
	// Every prelude name is missing: none is reserved since std declares
	// Path.
	if len(c.StdMissing()) != len(preludeNames) {
		t.Errorf("empty std: %d names missing, want %d", len(c.StdMissing()), len(preludeNames))
	}
	// The embedded std forms std.core and every prelude name is declared.
	// It checks clean through types; its only diagnostics are stage 7's
	// known list (TestStdFlowKnown), so units are judged through types
	// until that list is empty, then through Implemented().
	c, err = NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	want := Implemented()
	if len(c.Std().Report.Diags) != 0 {
		want = diag.PhaseTypes
	}
	for _, e := range c.Std().Report.Diags {
		if e.Diag.Phase != diag.PhaseFlow {
			t.Errorf("embedded std: a %v diagnostic: %s", e.Diag.Phase, c.Std().Report.Text(c.Std().Sources))
			break
		}
	}
	if c.Through() != want {
		t.Errorf("embedded std: Through %v, want %v; std: %s", c.Through(), want, c.Std().Report.Text(c.Std().Sources))
	}
	for _, n := range c.StdMissing() {
		if slices.Contains(preludeNames, n) {
			t.Errorf("embedded std: prelude name %s undeclared", n)
		}
	}
	// A full stand-in std: Implemented().
	c, err = NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Through() != Implemented() || len(c.Std().Report.Diags) != 0 || len(c.StdMissing()) != 0 {
		t.Errorf("full std: Through %v, std diagnostics %d, missing %q", c.Through(), len(c.Std().Report.Diags), c.StdMissing())
	}
	// A std without std.core: parse.
	files := fakeStdFiles()
	delete(files, "core/core.mk")
	c, _ = NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if c.Through() != diag.PhaseParse {
		t.Errorf("no std.core: Through %v", c.Through())
	}
	// A std with a names error: the phase before names.
	files = fakeStdFiles()
	files["fs/fs.mk"] = "export ReadText(): int {\n    return Nope()\n}\n"
	c, _ = NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if c.Through() != diag.PhaseImports || len(c.Std().Report.Diags) != 1 {
		t.Errorf("names error in std: Through %v, std %d diagnostics", c.Through(), len(c.Std().Report.Diags))
	}
	// A std with a types error (E0301, check's): the phase before types.
	files = fakeStdFiles()
	files["fs/fs.mk"] = "export ReadText(): int {\n    return \"x\"\n}\n"
	c, _ = NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if c.Through() != diag.PhaseNames || !slices.Equal(codesOf(c.Std().Report), []string{"E0301"}) {
		t.Errorf("types error in std: Through %v, std %q", c.Through(), codesOf(c.Std().Report))
	}
	// A std that does not parse: parse.
	files = fakeStdFiles()
	files["fs/fs.mk"] = "export ReadText(): int {\n    return (\n}\n"
	c, _ = NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if c.Through() != diag.PhaseParse {
		t.Errorf("parse error in std: Through %v", c.Through())
	}
}

// codesOf is a report's codes in order.
func codesOf(r diag.Report) []string {
	var out []string
	for _, e := range r.Diags {
		out = append(out, e.Diag.Code.String())
	}
	return out
}

const sharedPrelude = "export struct Point {\n    x: int\n}\n"

func TestCheckBlockSharesStd(t *testing.T) {
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	block := "F(p: Point): int {\n    return 1\n}\n\nexport G(): int {\n    return Missing()\n}\n"
	unit := func(name string) UnitInput {
		return UnitInput{Name: name, Prelude: "prelude.mk",
			Files: []UnitFile{{"block.mk", block}, {"prelude.mk", sharedPrelude}}}
	}
	var wg sync.WaitGroup
	outs := make([]UnitOutcome, 8)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outs[i] = c.CheckUnit(unit(fmt.Sprintf("doc/x.md#%d", i)))
		}(i)
	}
	wg.Wait()
	for i := 0; i < 4; i++ {
		outs = append(outs, c.CheckUnit(unit(fmt.Sprintf("doc/y.md#%d", i))))
	}
	for i, o := range outs {
		var got []string
		for _, e := range o.Report.Diags {
			got = append(got, o.Paths[e.Diag.At.File]+":"+diag.Location(o.Sources, e.Diag.At)[strings.LastIndexByte(
				diag.Location(o.Sources, e.Diag.At), '/')+1:]+":"+e.Diag.Code.String())
		}
		if !slices.Equal(got, []string{"block.mk:block.mk:6:12:E0201"}) {
			t.Errorf("unit %d: %q", i, got)
		}
	}
	// A repeated unit name is an internal error, never a crash.
	o := c.CheckUnit(unit("doc/x.md#0"))
	if len(o.Report.Diags) != 1 || o.Report.Diags[0].Diag.Code != diag.E0000 {
		t.Errorf("repeated name: %v", o.Report.Diags)
	}
}

func TestCheckUnitPaths(t *testing.T) {
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	o := c.CheckUnit(UnitInput{Name: "doc/reference.md#17", Prelude: "prelude.mk",
		Files: []UnitFile{{"block.mk", "export G(): int {\n    return 1\n}\n"}, {"prelude.mk", sharedPrelude}}})
	if len(o.Report.Diags) != 0 {
		t.Errorf("diagnostics: %s", o.Report.Text(o.Sources))
	}
	var got []string
	for id, p := range o.Paths {
		got = append(got, o.Sources.File(id).Path+"="+p)
	}
	slices.Sort(got)
	want := []string{"doc/reference.md#17/block.mk=block.mk", "doc/reference.md#17/prelude.mk=prelude.mk"}
	if !slices.Equal(got, want) || o.Through != Implemented() {
		t.Errorf("paths %q through %v", got, o.Through)
	}
}

func TestCheckUnitDirectoryUnit(t *testing.T) {
	// A conformance directory unit: modules named relative to the unit root,
	// keyed under the unit's name; tests/conformance/codes/E0205.
	c, _ := NewCorpusSession(Config{StdDir: fakeStd(t)})
	o := c.CheckUnit(UnitInput{Name: "tests/conformance/codes/E0205", Files: []UnitFile{
		{"a/a.mk", "import b\n\nexport One(): int {\n    return b.Two() - 1\n}\n"},
		{"b/b.mk", "import a\n\nexport Two(): int {\n    return 2\n}\n"},
		{"main.mk", "import a\n\nGet(): int {\n    return a.One()\n}\n"},
	}})
	if len(o.Report.Diags) != 1 || o.Report.Diags[0].Diag.Code != diag.E0205 || o.Paths[o.Report.Diags[0].Diag.At.File] != "a/a.mk" {
		t.Errorf("got %s", o.Report.Text(o.Sources))
	}
	// Two units with the same module names do not collide.
	for _, name := range []string{"u1", "u2"} {
		o := c.CheckUnit(UnitInput{Name: name, Files: []UnitFile{
			{"main.mk", "import util\n\nexport F(): int {\n    return util.U()\n}\n"},
			{"util/u.mk", "export U(): int {\n    return 1\n}\n"},
		}})
		if len(o.Report.Diags) != 0 {
			t.Errorf("%s: %s", name, o.Report.Text(o.Sources))
		}
	}
}

func TestCheckUnitParseOnlyAndPending(t *testing.T) {
	// With an empty std, a names-phase unit reports nothing past parse.
	c, _ := NewCorpusSession(Config{StdDir: m1Std(t)})
	o := c.CheckUnit(UnitInput{Name: "doc/reference.md#17", Prelude: "prelude.mk",
		Files: []UnitFile{{"block.mk", "export Main(sys: &System) {\n    Print(1)\n}\n"}, {"prelude.mk", sharedPrelude}}})
	if len(o.Report.Diags) != 0 || o.Through != diag.PhaseParse {
		t.Errorf("empty std: %s through %v", o.Report.Text(o.Sources), o.Through)
	}
	// A parse unit accepts bodiless functions and reports parse errors.
	o = c.CheckUnit(UnitInput{Name: "p1", ParseOnly: true, Files: []UnitFile{{"block.mk", "F(): int\nG() {\n    x++\n}\n"}}})
	if len(o.Report.Diags) != 1 || o.Report.Diags[0].Diag.Code != diag.E0101 {
		t.Errorf("parse unit: %s", o.Report.Text(o.Sources))
	}
}

// TestStdStateMissing: StdState carries std's report and, sorted, the
// missing prelude names and layer 0 to 2 module paths; a unit's Skipped is
// empty, because no stage past Available() is entered.
func TestStdStateMissing(t *testing.T) {
	// The empty std forms no module: every prelude name (Path too, which is
	// not reserved) and every layer 0 to 2 module is missing.
	c, err := NewCorpusSession(Config{StdDir: m1Std(t)})
	if err != nil {
		t.Fatal(err)
	}
	st := c.StdState()
	if want := len(preludeNames) + len(stdLayerModules); len(st.Missing) != want {
		t.Errorf("empty std: %d missing, want %d: %q", len(st.Missing), want, st.Missing)
	}
	if !slices.IsSorted(st.Missing) || !slices.Contains(st.Missing, "Print") || !slices.Contains(st.Missing, "std.core") ||
		!slices.Contains(st.Missing, "Path") {
		t.Errorf("empty std: Missing %q", st.Missing)
	}
	// The embedded std: Missing is sorted, holds no prelude name and only
	// layer 0 to 2 module paths, and std.core is not among them.
	c, err = NewCorpusSession(Config{})
	if err != nil {
		t.Fatal(err)
	}
	st = c.StdState()
	if !slices.IsSorted(st.Missing) || slices.Contains(st.Missing, "std.core") {
		t.Errorf("embedded std: Missing %q", st.Missing)
	}
	for _, n := range st.Missing {
		if slices.Contains(preludeNames, n) || !slices.Contains(stdLayerModules, n) {
			t.Errorf("embedded std: Missing holds %s", n)
		}
	}
	// The full stand-in std declares the prelude and forms std.core, std.fs
	// and std.sys (raw is no layer 0 to 2 module): only the other modules
	// are missing.
	c, err = NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	st = c.StdState()
	want := []string{"std.atomic", "std.bits", "std.coll", "std.log", "std.math", "std.mem", "std.net",
		"std.proc", "std.rt", "std.simd", "std.sync", "std.target", "std.task", "std.text", "std.time"}
	if !slices.Equal(st.Missing, want) {
		t.Errorf("stand-in std: Missing %q, want %q", st.Missing, want)
	}
	if len(st.Report.Diags) != len(c.Std().Report.Diags) {
		t.Errorf("StdState.Report has %d diagnostics, Std() %d", len(st.Report.Diags), len(c.Std().Report.Diags))
	}
	st.Missing[0] = "changed"
	if c.StdState().Missing[0] == "changed" {
		t.Error("StdState shares its Missing slice with the session")
	}
	// A names error in std is in its report.
	files := fakeStdFiles()
	files["fs/fs.mk"] = "export ReadText(): int {\n    return Nope()\n}\n"
	c, _ = NewCorpusSession(Config{StdDir: writeTree(t, files)})
	if r := c.StdState().Report; len(r.Diags) != 1 || r.Diags[0].Diag.Code != diag.E0201 {
		t.Errorf("names error in std: StdState.Report %d diagnostics", len(r.Diags))
	}
	// Skipped is empty for checked and parse-only units alike.
	for i, u := range []UnitInput{
		{Name: "u/check", Files: []UnitFile{{Path: "block.mk", Text: "F(): int {\n    return Nope()\n}\n"}}},
		{Name: "u/parse", Files: []UnitFile{{Path: "block.mk", Text: "F(): int\n"}}, ParseOnly: true},
	} {
		var r BlockResult = c.CheckUnit(u)
		if r.Skipped != 0 {
			t.Errorf("unit %d: Skipped %v, want empty", i, r.Skipped.Stages())
		}
	}
}
