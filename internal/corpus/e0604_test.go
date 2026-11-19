// Why: when a static of global memory is stored into a parameter, the
// checker must report the one E0604 no clause can fix, not a misleading
// clause fix and a follow-on E0605.
// What: TestStaticE0604WinsAndDropsItsE0605 on testdata/r5/r5b.mk.
// How: checks the program and pins the diagnostics at both spans.
//
// The rule lives in internal/borrow's report.go (finish) and solve.go
// (staticStore). A static of global memory stored into a parameter's node
// is an E0604 no clause can fix. At its span, the E0605 that only follows
// from it is dropped, and the static E0604 is kept over the clause E0604
// there, whose fix would leave it. Without the rule, the same text gave a
// clause E0604 and an E0605 at 17:13 and a clause E0604 at 29:5.

package corpus

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
)

func TestStaticE0604WinsAndDropsItsE0605(t *testing.T) {
	root := repoRoot(t)
	text, err := readText(filepath.Join(root, "internal", "corpus", "testdata", "r5", "r5b.mk"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := makeUnit("internal/corpus/testdata/r5/r5b.mk", "soundness", true, []UnitFile{{Path: "main.mk", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	u.Name = fmt.Sprintf("%s#%d", u.Name, soundUnitN.Add(1))
	pre, err := Prelude(root)
	if err != nil {
		t.Fatal(err)
	}
	s := soundSess(t)
	r := s.CheckUnit(ToUnitInput(&u, pre, driver.UnitCheck))
	var got []string
	for _, d := range r.Report.Diags {
		lc := r.Sources.LineCol(d.Diag.At.StartPos())
		got = append(got, fmt.Sprintf("%d:%d %s", lc.Line, lc.Col, d.Diag.Code))
		if d.Diag.Code != diag.E0604 {
			continue
		}
		// The static form names global memory and offers no fix; the clause
		// form ("a loan of it into it, which the signature does not
		// declare") is the one the rule hides at the same span.
		if !strings.Contains(d.Diag.Message, "into global memory") || strings.Contains(d.Diag.Message, "does not declare") {
			t.Errorf("%d:%d: E0604 is not the static form: %s", lc.Line, lc.Col, d.Diag.Message)
		}
		for _, f := range d.Diag.Fixes {
			t.Errorf("%d:%d: the static E0604 offers a fix (%s): %s", lc.Line, lc.Col, f.Class, f.Why)
		}
	}
	want := []string{"16:19 E0501", "17:13 E0604", "29:5 E0604"}
	if !slices.Equal(got, want) {
		t.Errorf("diagnostics %v, want %v:\n%s", got, want, r.Report.Text(r.Sources))
	}
}
