// Why: the shallow-loan rule (a loan stored from a value whose type is
// shallow stays shallow where it lands) changes verdicts, and its witness
// programs pin the new ones.
// What: TestWitnessFiles, TestAsBefore and TestWitnesses over
// tests/soundness/witness/fld3/.
// How: each file is a one-file unit with the test prelude, judged by its
// first line and its markers.
//
// Every file of tests/soundness/witness/fld3/ is a one-file unit with the
// test prelude, as tests/soundness/witness/'s are. Its first line and its
// markers are the verdict measured on the compiler with the shallow-loan
// rule and the two borrow-hole fixes on, which TestWitnesses judges;
// its third line, "// before FLD-3: ...", records the verdict of the
// compiler without the rule, with its lines. No compiler of the tree can
// judge the third line any more, so only its form is checked
// (TestWitnessFiles).

package corpus

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/driver"
)

const beforeMarkerPrefix = "// before FLD-3: "

// fieldLoansFiles lists tests/soundness/witness/fld3/*.mk in name order.
func fieldLoansFiles(t testing.TB) []string {
	t.Helper()
	ps, err := filepath.Glob(filepath.Join(soundRoot(t), "witness", "fld3", "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ps)
	return ps
}

// fieldLoansAsBefore rewrites a witness to its "before" verdict: the first
// line becomes that verdict, every marker is removed, and the before line's
// markers are written on their lines. The before line reads "ok" or
// "reject <codes> at <line> <code>, <line> <code>", then an optional
// parenthesis.
func fieldLoansAsBefore(text string) (string, error) {
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[2], beforeMarkerPrefix) {
		return "", fmt.Errorf("line 3 is not %q", strings.TrimSpace(beforeMarkerPrefix))
	}
	spec := strings.TrimPrefix(lines[2], beforeMarkerPrefix)
	if i := strings.Index(spec, " ("); i >= 0 {
		spec = spec[:i]
	}
	marks := map[int][]string{}
	verdict := "ok"
	if spec != "ok" {
		head, at, ok := strings.Cut(spec, " at ")
		if !ok || !strings.HasPrefix(head, "reject ") {
			return "", fmt.Errorf("bad before line: %s", lines[2])
		}
		verdict = head
		for _, m := range strings.Split(at, ", ") {
			ws := strings.Fields(m)
			if len(ws) != 2 {
				return "", fmt.Errorf("bad marker %q in %s", m, lines[2])
			}
			n, err := strconv.Atoi(ws[0])
			if err != nil || n <= 3 || n > len(lines) {
				return "", fmt.Errorf("bad line %q in %s", ws[0], lines[2])
			}
			marks[n] = append(marks[n], ws[1])
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if i > 2 {
			if j := strings.LastIndex(l, " // <- "); j >= 0 {
				l = l[:j]
			}
		}
		if cs, ok := marks[i+1]; ok {
			l += " // <- " + strings.Join(cs, " ")
		}
		out[i] = l
	}
	out[0] = "// verdict: " + verdict
	return strings.Join(out, "\n"), nil
}

// fieldLoansUnit makes the one-file unit of a witness text.
func fieldLoansUnit(name, text string) (Unit, error) {
	return makeUnit("tests/soundness/witness/fld3/"+name, "soundness", true, []UnitFile{{Path: name, Text: text}})
}

// judgeFieldLoans judges one witness text as judgeWitness judges a file.
func judgeFieldLoans(t testing.TB, s *driver.CorpusSession, name, text string) witnessJudged {
	t.Helper()
	u, err := fieldLoansUnit(name, text)
	if err != nil {
		t.Fatal(err)
	}
	u.Name = fmt.Sprintf("%s#%d", u.Name, soundUnitN.Add(1))
	pre, err := Prelude(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	r := s.CheckUnit(ToUnitInput(&u, pre, driver.UnitCheck))
	j := JudgeUnit(&u, r, s.StdState(), driver.Available())
	out := witnessJudged{File: name, Status: "pass", Reason: j.Reason}
	if j.Status != StatusPass {
		out.Status = "fail"
	}
	for _, d := range r.Report.Diags {
		out.Emitted = append(out.Emitted, d.Diag.Code.String())
	}
	return out
}

// TestWitnessFiles: every witness parses as a unit, by its own verdict
// and by its before line; no compiler runs.
func TestWitnessFiles(t *testing.T) {
	files := fieldLoansFiles(t)
	if len(files) == 0 {
		t.Fatal("no witness under tests/soundness/witness/fld3")
	}
	for _, p := range files {
		text, err := readText(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(p)
		if _, err := fieldLoansUnit(name, text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		before, err := fieldLoansAsBefore(text)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if _, err := fieldLoansUnit(name, before); err != nil {
			t.Errorf("%s, before FLD-3: %v", name, err)
		}
	}
}

// TestAsBefore: the rewrite keeps every line but the verdict and the
// markers.
func TestAsBefore(t *testing.T) {
	in := "// verdict: reject E0602\n// note: n\n// before FLD-3: reject E0602 E0604 at 5 E0604, 6 E0602 (measured)\nF() {\n    a() // <- E0602\n    b()\n}\n"
	want := "// verdict: reject E0602 E0604\n// note: n\n// before FLD-3: reject E0602 E0604 at 5 E0604, 6 E0602 (measured)\nF() {\n    a() // <- E0604\n    b() // <- E0602\n}\n"
	if got, err := fieldLoansAsBefore(in); err != nil || got != want {
		t.Fatalf("fieldLoansAsBefore = %q, %v; want %q", got, err, want)
	}
	ok := "// verdict: ok\n// note: n\n// before FLD-3: ok (measured)\nF() {\n}\n"
	if got, err := fieldLoansAsBefore(ok); err != nil || got != ok {
		t.Fatalf("fieldLoansAsBefore(ok) = %q, %v", got, err)
	}
	for _, bad := range []string{"// verdict: ok\n", "// verdict: ok\n// n\n// before FLD-3: reject E0604\n", "// verdict: ok\n// n\n// before FLD-3: reject E0604 at 2 E0604\nF()\n"} {
		if _, err := fieldLoansAsBefore(bad); err == nil {
			t.Errorf("fieldLoansAsBefore accepted %q", bad)
		}
	}
}

// TestWitnesses: every witness meets its verdict on the compiler as
// built, which has the shallow-loan rule.
func TestWitnesses(t *testing.T) {
	s := soundSess(t)
	for _, p := range fieldLoansFiles(t) {
		text, err := readText(p)
		if err != nil {
			t.Fatal(err)
		}
		if j := judgeFieldLoans(t, s, filepath.Base(p), text); j.Status != "pass" {
			t.Errorf("%s: %s", j.File, j.Reason)
		}
	}
}
