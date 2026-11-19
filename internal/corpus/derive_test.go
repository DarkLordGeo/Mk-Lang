// Why: the output of gen derive.Eq must read like handwritten code.
// What: TestDeriveEqReadsLikeHandwritten against testdata/derive_eq.golden.
// How: generates Eq for doc/reference.md's User, compares with the golden,
// and checks the output pasted in place of the gen line and its layout.
//
// The reference's User with `gen derive.Eq(User)`, checked through the
// corpus session with std's derive.Eq, prints exactly
// testdata/derive_eq.golden. That golden is reviewed by hand when it changes
// (go test -run TestDeriveEqReadsLikeHandwritten -update rewrites it). The
// test also checks what "like handwritten" can mean mechanically: the
// output pasted in place of the gen line checks clean as handwritten code,
// and its text keeps the house layout (4-space indents, no tabs, no trailing
// blanks, no doubled blank lines, a trace comment on the item).

package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/driver"
	"mk/internal/types"
)

const deriveEqGolden = "testdata/derive_eq.golden"

// referenceUser is doc/reference.md's `struct User { ... }`, cut from the
// doc block that declares it, so the test follows the reference.
func referenceUser(t *testing.T) string {
	t.Helper()
	blocks, err := DesignBlocks(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.Document != "doc/reference.md" {
			continue
		}
		s := "\n" + b.Source
		i := strings.Index(s, "\nstruct User {\n")
		if i < 0 {
			continue
		}
		rest := s[i+1:]
		return rest[:strings.Index(rest, "\n}\n")+3]
	}
	t.Fatal("doc/reference.md has no block declaring struct User")
	return ""
}

func TestDeriveEqReadsLikeHandwritten(t *testing.T) {
	target, ok := types.TargetByName("x86_64-windows") // the golden's header names it
	if !ok {
		t.Fatal("no target x86_64-windows")
	}
	comp := DriverCompiler(repoRoot(t), driver.Config{Target: target})
	info, err := comp.Start()
	if err != nil {
		t.Fatal(err)
	}
	user := referenceUser(t)
	src := "import std.derive\n\n" + user + "\ngen derive.Eq(User)\n"
	u := &Unit{Key: "derive/eq", Name: "derive/eq", Verdict: VerdictOK,
		Files: []UnitFile{{Path: "main.mk", Text: src}}}
	res := comp.Check(u, driver.UnitCheck)
	if len(res.Report.Diags) > 0 || res.Skipped != 0 {
		t.Fatalf("the gen unit does not check clean (skipped %v):\n%s", res.Skipped.Stages(), res.Report.Text(res.Sources))
	}
	if j := JudgeUnit(u, res, info.State, comp.Available()); j.Status != StatusPass {
		t.Fatalf("the gen unit is %v (%s), want PASS", j.Status, j.Reason)
	}
	genPath := u.Name + "/gen/" + target.Name + "/gen.mk"
	f, ok := res.Sources.ByPath(genPath)
	if !ok {
		t.Fatalf("no generated file %s in the session's sources", genPath)
	}
	got := string(f.Text)

	golden := filepath.FromSlash(deriveEqGolden)
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s; review it by hand", golden)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update once, then review the file)", err)
	}
	if got != string(want) {
		t.Errorf("gen derive.Eq(User) output differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
	}

	// The house layout, line by line.
	if !strings.Contains(got, "\n// gen derive.Eq#1 for User\nimpl Eq for User {\n") {
		t.Errorf("the impl has no trace comment `// gen derive.Eq#1 for User` above it")
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	for i, l := range lines {
		ind := len(l) - len(strings.TrimLeft(l, " "))
		switch {
		case strings.ContainsRune(l, '\t'):
			t.Errorf("line %d has a tab: %q", i+1, l)
		case strings.TrimRight(l, " ") != l:
			t.Errorf("line %d has trailing blanks: %q", i+1, l)
		case ind%4 != 0:
			t.Errorf("line %d is indented %d spaces, not a multiple of 4: %q", i+1, ind, l)
		case len(l) > 100:
			t.Errorf("line %d is %d bytes long", i+1, len(l))
		case l == "" && i > 0 && lines[i-1] == "":
			t.Errorf("line %d: two blank lines in a row", i+1)
		}
	}

	// Handwritten: the generated items in place of the gen line check clean.
	items := got[strings.Index(got, "\n// gen ")+1:]
	hand := "\n" + user + "\n" + items
	hu := &Unit{Key: "derive/eq-by-hand", Name: "derive/eq-by-hand", Verdict: VerdictOK,
		Files: []UnitFile{{Path: "main.mk", Text: hand}}}
	hres := comp.Check(hu, driver.UnitCheck)
	if len(hres.Report.Diags) > 0 {
		t.Errorf("the output written by hand does not check clean:\n%s\n%s", hand, hres.Report.Text(hres.Sources))
	}
}
