// Why: every conformance unit is a test, so the loader that reads their
// headers and the run that judges them must both be exact.
// What: the loader tests (TestUnitHeaderNeedsAndPrelude,
// TestUnitsLoadsDiffSuites and the header-rule tests), TestConformance and
// TestConformanceDeterministic.
// How: synthetic trees written by confTree for the loader, the real
// tests/conformance for the judged run.

package corpus

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/driver"
	"mk/internal/source"
)

// confTree writes files under root/tests/conformance plus the root marker,
// and returns root.
func confTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["../../doc/reference.md"] = "# ref\n"
	for _, s := range Suites {
		if err := os.MkdirAll(filepath.Join(root, "tests", "conformance", s.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, text := range files {
		full := filepath.Join(root, "tests", "conformance", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestUnitHeaderNeedsAndPrelude(t *testing.T) {
	root := confTree(t, map[string]string{
		"codes/E0204.mk":       "// verdict: reject E0204\n// needs: names\n// note: G3 form\nF() {\n    x // <- E0204\n}\n",
		"codes/E0201.mk":       "// verdict: reject E0201\n// prelude: yes\nx // <- E0201\n",
		"rules/R1_accept_a.mk": "// verdict: ok\n// rule: R1\n// also: H05\nF() { }\n",
		"rules/R1_reject_b.mk": "// verdict: reject E0501\n// prelude: no\n// rule: EV533\nx // <- E0501\n",
		"model/R1.Copies.mk":   "// verdict: ok\n// case: R1.Copies\nF() { }\n",
		"codes/E0205/main.mk":  "// verdict: reject E0205\nimport a\n",
		"codes/E0205/a/a.mk":   "import main // <- E0205\n",
		"codes/E0205/pkg.mk":   "const Package = 1\n",
		"codes/E0204.json":     "{}\n",
	})
	us, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Unit{}
	var keys []string
	for _, u := range us {
		by[u.Key] = u
		keys = append(keys, u.Key)
	}
	want := []string{"tests/conformance/codes/E0201.mk", "tests/conformance/codes/E0204.mk", "tests/conformance/codes/E0205",
		"tests/conformance/model/R1.Copies.mk", "tests/conformance/rules/R1_accept_a.mk", "tests/conformance/rules/R1_reject_b.mk"}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Fatalf("units %v\nwant %v", keys, want)
	}
	if u := by[want[1]]; u.Meta.Needs != diag.PhaseNames || u.Meta.Prelude || u.Meta.Note != "G3 form" ||
		len(u.Expected) != 1 || u.Expected[0] != (UnitMarker{File: "E0204.mk", Line: 5, Code: diag.E0204}) {
		t.Errorf("E0204 unit = %+v", u)
	}
	if !by[want[0]].Meta.Prelude {
		t.Errorf("prelude: yes ignored")
	}
	if u := by[want[4]]; !u.Meta.Prelude || len(u.Meta.Rule) != 1 || u.Meta.Rule[0] != "R1" || len(u.Meta.Also) != 1 {
		t.Errorf("rules default prelude or headers: %+v", u.Meta)
	}
	if by[want[5]].Meta.Prelude {
		t.Errorf("prelude: no ignored")
	}
	if by[want[3]].Meta.Case != "R1.Copies" || !by[want[3]].Meta.Prelude {
		t.Errorf("model unit meta %+v", by[want[3]].Meta)
	}
	d := by[want[2]]
	if len(d.Files) != 3 || d.Files[0].Path != "main.mk" || d.Files[1].Path != "a/a.mk" || d.Files[2].Path != "pkg.mk" ||
		len(d.Expected) != 1 || d.Expected[0] != (UnitMarker{File: "a/a.mk", Line: 1, Code: diag.E0205}) {
		t.Errorf("directory unit = %+v", d)
	}
	for name, text := range map[string]string{
		"needs on ok":     "// verdict: ok\n// needs: names\n",
		"needs later":     "// verdict: reject E0501\n// needs: later\nx // <- E0501\n",
		"prelude maybe":   "// verdict: ok\n// prelude: maybe\n",
		"unknown key":     "// verdict: ok\n// colour: blue\n",
		"case in rules":   "// verdict: ok\n// case: R1.X\n",
		"bad rule":        "// verdict: ok\n// rule: whatever\n",
		"no verdict":      "F() { }\n",
		"marker not code": "// verdict: reject E0501\nx // <- E0502\n",
	} {
		r := confTree(t, map[string]string{"rules/x.mk": text})
		_, err := Units(r)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), "tests/conformance/rules/x.mk:") {
			t.Errorf("%s: error %q does not name path:line", name, err)
		}
	}
}

func TestUnitsLoadsDiffSuites(t *testing.T) {
	us, err := Units(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, u := range us {
		if strings.HasSuffix(u.Key, ".json") || strings.HasSuffix(u.Key, ".golden") || strings.HasSuffix(u.Key, ".txt") {
			t.Errorf("data file loaded as a unit: %s", u.Key)
		}
		if strings.HasPrefix(u.Key, "tests/conformance/diff/") {
			found[u.Key] = true
			if u.Verdict != VerdictOK || u.Meta.Prelude {
				t.Errorf("%s: verdict %s prelude %v, want ok without the prelude", u.Key, u.Verdict, u.Meta.Prelude)
			}
		}
	}
	for _, k := range []string{"tests/conformance/diff/eval.mk", "tests/conformance/diff/layout.mk"} {
		if !found[k] {
			t.Errorf("%s not loaded", k)
		}
	}
	t.Logf("%d conformance units", len(us))
	if _, err := Units(confTree(t, map[string]string{"notes/a.mk": "// verdict: ok\n"})); err == nil {
		t.Errorf("a stray notes/ suite was accepted")
	}
	if _, err := Units(confTree(t, map[string]string{"stray.mk": "// verdict: ok\n"})); err == nil {
		t.Errorf("a .mk file directly under tests/conformance was accepted")
	}
}

// TestConformance judges every unit (one subtest per unit) with NoPending:
// no FAIL outside knownUnitFailures, and so no PENDING that
// pending.txt does not explain, since NoPending makes one FAIL "unexplained
// pending: <reason>".
func TestConformance(t *testing.T) {
	r, err := Run(Options{Conformance: true, Only: "tests/conformance/*/*", NoPending: true})
	if err != nil {
		t.Fatal(err)
	}
	judged := map[string]bool{}
	for _, u := range r.Units {
		judged[u.Unit.Key] = true
		t.Run(u.Unit.Key, func(t *testing.T) {
			if why, ok := knownUnitFailures[u.Unit.Key]; ok {
				if u.Status != StatusFail {
					t.Errorf("known failure (%s) is %s now: remove it from knownUnitFailures", why, u.Status)
				}
				return
			}
			if u.Status == StatusFail {
				t.Errorf("FAIL: %s", u.Reason)
			}
			if u.Status == StatusPending && u.Reason == "" {
				t.Errorf("PENDING without a reason")
			}
			checkStage6(t, u, r)
		})
	}
	for k, why := range knownUnitFailures {
		if !judged[k] {
			t.Errorf("knownUnitFailures names %s (%s), which is not a conformance unit", k, why)
		}
	}
	s := r.Summary
	t.Logf("%d conformance units: pass %d, fail %d, pending %d", s.Units, s.Pass, s.Fail, s.Pending)
}

// TestE1206CForOneDiagnostic pins the syntax fix that took codes/E1206_c_for
// off knownUnitFailures: a C-style for head (Name := ...;) gets exactly one
// E1206, on the for line, and no E0101 beside it.
func TestE1206CForOneDiagnostic(t *testing.T) {
	const key = "tests/conformance/codes/E1206_c_for.mk"
	r, err := Run(Options{Conformance: true, Only: key})
	if err != nil {
		t.Fatal(err)
	}
	var u *UnitResult
	for i := range r.Units {
		if r.Units[i].Unit.Key == key {
			u = &r.Units[i]
		}
	}
	if u == nil {
		t.Fatalf("%s not judged", key)
	}
	if u.Status != StatusPass {
		t.Errorf("%s is %s (%s), want PASS", key, u.Status, u.Reason)
	}
	if len(u.Emitted) != 1 || u.Emitted[0].Code.String() != "E1206" || u.Emitted[0].Line != 5 {
		t.Errorf("%s emitted %+v, want one E1206 on line 5", key, u.Emitted)
	}
}

// knownUnitFailures are conformance units that fail for a reason outside
// this package, with that reason.
var knownUnitFailures = map[string]string{}

func TestConformanceDeterministic(t *testing.T) {
	var out [2][]byte
	for i, jobs := range []int{1, 8} {
		r, err := Run(Options{Conformance: true, Jobs: jobs})
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := WriteJSON(&b, r); err != nil {
			t.Fatal(err)
		}
		out[i] = b.Bytes()
	}
	if !bytes.Equal(out[0], out[1]) {
		t.Errorf("conformance report differs between -j 1 and -j 8")
	}
}

// A "// rule:" value is a rule or case ID of doc/memory.md (R, H or X and a
// number) or another requirement ID, hyphenated ones included (MMA-031).
func TestRuleHeaderAcceptsChecklistIDs(t *testing.T) {
	for _, id := range []string{"R10", "H05", "X03", "EV533", "REF017", "GR102", "S-G208", "MMA-031", "IR-297", "REF-001", "DRV-01", "G-12"} {
		if !ruleID.MatchString(id) {
			t.Errorf("rule %s refused", id)
		}
	}
	for _, id := range []string{"whatever", "r10", "123", "MMA-", "MMA--031", "-031", "R10a", "ABCDE-1"} {
		if ruleID.MatchString(id) {
			t.Errorf("rule %s accepted", id)
		}
	}
	root := confTree(t, map[string]string{"rules/R1_accept_x.mk": "// verdict: ok\n// rule: MMA-031\nF() { }\n"})
	us, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 1 || len(us[0].Meta.Rule) != 1 || us[0].Meta.Rule[0] != "MMA-031" {
		t.Errorf("units %+v, want one with rule MMA-031", us)
	}
}

// "// needs:" must name a group some pass emits a verdict code in
// (EmittedIn), so the judge always honours it; E0101's names form (resolve)
// is one, E0501 has no names form.
func TestNeedsMustNameAnEmittingGroup(t *testing.T) {
	ok := map[string]string{
		"codes/E0101_const_pattern.mk": "// verdict: reject E0101\n// needs: names\nx // <- E0101\n",
		"codes/E0204.mk":               "// verdict: reject E0204\n// needs: types\nx // <- E0204\n",
		"codes/E1204.mk":               "// verdict: reject E1204\n// needs: parse\nx // <- E1204\n",
		"codes/E0201.mk":               "// verdict: reject E0201\n// needs: names\nx // <- E0201\n",
	}
	want := len(ok) // confTree adds the root marker to the map
	us, err := Units(confTree(t, ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != want {
		t.Fatalf("%d units, want %d", len(us), want)
	}
	for _, u := range us {
		if u.Meta.Needs == diag.PhaseInternal {
			t.Errorf("%s: needs dropped", u.Key)
		}
	}
	for name, text := range map[string]string{
		"E0501 in names": "// verdict: reject E0501\n// needs: names\nx // <- E0501\n",
		// E0101 has a types row, so E0102 stands in.
		"E0102 in types": "// verdict: reject E0102\n// needs: types\nx // <- E0102\n",
		"E0301 in parse": "// verdict: reject E0301\n// needs: parse\nx // <- E0301\n",
	} {
		_, err := Units(confTree(t, map[string]string{"codes/x.mk": text}))
		if err == nil || !strings.Contains(err.Error(), "tests/conformance/codes/x.mk:2:") {
			t.Errorf("%s: err %v, want a refusal naming tests/conformance/codes/x.mk:2", name, err)
		}
	}
}

// Unit's invariant: prelude.mk is the test prelude's
// file name, so no unit file may have it, in a file unit or a directory
// unit; ToUnitInput refuses a hand-made unit that breaks the invariant.
func TestPreludeFileNameIsReserved(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"directory unit": {"rules/R1_accept_x/main.mk": "// verdict: ok\nF() { }\n", "rules/R1_accept_x/prelude.mk": "G() { }\n"},
		"file unit":      {"rules/prelude.mk": "// verdict: ok\nF() { }\n"},
	} {
		_, err := Units(confTree(t, files))
		if err == nil || !strings.Contains(err.Error(), PreludeFile+" is reserved") {
			t.Errorf("%s: err %v, want prelude.mk reserved", name, err)
		}
	}
	// A nested a/prelude.mk is a different unit-relative path: allowed.
	if _, err := Units(confTree(t, map[string]string{"rules/R1_accept_y/main.mk": "// verdict: ok\nF() { }\n",
		"rules/R1_accept_y/a/prelude.mk": "G() { }\n"})); err != nil {
		t.Errorf("a/prelude.mk refused: %v", err)
	}
	u := &Unit{Key: "k", Name: "k", Verdict: VerdictOK, Meta: Meta{Prelude: true},
		Files: []UnitFile{{Path: "main.mk", Text: "F() { }\n"}, {Path: PreludeFile, Text: "G() { }\n"}}}
	ie := source.Guard("ToUnitInput", source.Span{}, func() { ToUnitInput(u, "P\n", driver.UnitCheck) })
	if ie == nil {
		t.Errorf("ToUnitInput accepted a unit with its own %s", PreludeFile)
	}
	u.Meta.Prelude = false
	if in := ToUnitInput(u, "P\n", driver.UnitCheck); len(in.Files) != 2 || in.Prelude != "" {
		t.Errorf("prelude off: %+v", in)
	}
}

// Regression for a finding on codes/E0903 (and E0905, E0906): once the
// embedded std reached types, names ran on their generator modules and main
// modules while stage 4 was stubbed (Skipped holds StageGen), and rule 9
// judged the E0201s FAIL (`Gen`, `Type`, and the type `Made` the gen line
// would declare). While gen is not wired they must be PENDING needs StageGen
// (rule 7b); once gen is wired, rule 7b no longer applies and each must PASS
// on its markers.
func TestGenUnitsWaitForGen(t *testing.T) {
	r := fullRun(t)
	want := map[string]bool{
		"tests/conformance/codes/E0903": false,
		"tests/conformance/codes/E0905": false,
		"tests/conformance/codes/E0906": false,
	}
	for _, u := range r.Units {
		if _, ok := want[u.Unit.Key]; !ok {
			continue
		}
		want[u.Unit.Key] = true
		switch {
		case r.Avail.Has(driver.StageGen):
			// Gen is wired, so rule 7b does not hold the unit and its
			// markers decide.
			if u.Status != StatusPass {
				t.Errorf("%s is %s %q with gen wired, want PASS", u.Unit.Key, u.Status, u.Reason)
			}
		case u.Status != StatusPending || u.Reason != "needs StageGen":
			t.Errorf("%s is %s %q, want PENDING \"needs StageGen\" while gen is stubbed", u.Unit.Key, u.Status, u.Reason)
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("%s not judged", k)
		}
	}
}

// TestUnitsSkipsBuildCache: mk writes a gitignored
// .mk-cache next to any root it checks or builds, so one can appear in a
// suite or beside the suites. Units skips every dot-name; before the fix a
// suite's .mk-cache aborted the whole load with "a directory unit needs
// main.mk".
func TestUnitsSkipsBuildCache(t *testing.T) {
	root := confTree(t, map[string]string{
		"codes/E0204.mk":          "// verdict: reject E0204\n// needs: names\nF() {\n    x // <- E0204\n}\n",
		"codes/.mk-cache/v1/blob": "cache\n",
		"rules/.mk-cache/x.mk":    "not a unit\n",
		".mk-cache/v1/blob":       "cache\n",
		"model/.hidden/main.mk":   "// verdict: ok\nF() { }\n",
	})
	us, err := Units(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 1 || us[0].Key != "tests/conformance/codes/E0204.mk" {
		var keys []string
		for _, u := range us {
			keys = append(keys, u.Key)
		}
		t.Fatalf("units %q, want only tests/conformance/codes/E0204.mk", keys)
	}
}
