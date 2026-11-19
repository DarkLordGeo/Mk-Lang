// Why: a documentation block may show unsafe code, which a package must
// permit, so the corpus judges it inside a package that does.
// What: tests that a doc block is judged as the one module of a package
// whose pkg.mk lists it in unsafe_modules, so its unsafe block or section
// clause draws no E1102, while a conformance unit with no manifest still does.
// How: checks both kinds of unit through a corpus session and looks for E1102.

package corpus

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

func TestDocBlockIsUnsafeModule(t *testing.T) {
	c := DriverCompiler(repoRoot(t), driver.Config{})
	info, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if info.Through < diag.PhaseTypes {
		t.Skipf("session judges only through %v", info.Through)
	}
	hasE1102 := func(u *Unit, r driver.BlockResult) bool {
		for _, g := range Got(u, r) {
			if g.Code == diag.E1102 {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct{ name, src string }{
		{"unsafe", "Peek(p: *u8): u8 {\n    unsafe \"x\" {\n        return *p\n    }\n}\n"},
		{"section", "const Header: u32 = 1 section \"s\"\n"},
	} {
		b := &Block{Document: "doc/d294-" + tc.name + ".md", Line: 3, Verdict: VerdictOK, Source: tc.src}
		u := BlockUnit(b)
		if in := ToUnitInput(u, "", driver.UnitCheck); !in.Design {
			t.Errorf("%s: a design block's input has Design false", tc.name)
		}
		r := c.Check(u, driver.UnitCheck)
		if hasE1102(u, r) {
			t.Errorf("%s: a doc block drew E1102", tc.name)
		}
		j := JudgeUnit(u, r, info.State, c.Available())
		if j.Status != StatusPass {
			t.Errorf("%s: design block is %s (%s), want PASS", tc.name, j.Status, j.Reason)
		}
	}
	// A conformance unit with no manifest permits no module.
	cu, err := makeUnit("tests/conformance/codes/d294.mk", "conformance", true, []UnitFile{{Path: "d294.mk",
		Text: "// verdict: reject E1102\nPeek(p: *u8): u8 {\n    unsafe \"x\" { // <- E1102\n        return *p\n    }\n}\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if in := ToUnitInput(&cu, "", driver.UnitCheck); in.Design {
		t.Error("a conformance unit's input has Design true")
	}
	if !hasE1102(&cu, c.Check(&cu, driver.UnitCheck)) {
		t.Error("a conformance unit without unsafe_modules drew no E1102")
	}
}
