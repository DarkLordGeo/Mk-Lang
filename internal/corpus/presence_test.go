// Why: the conformance suite must keep a test for every error code, every
// memory-model rule, every hard case and every model case.
// What: TestEveryCodeHasATest, TestEveryRuleHasAcceptAndReject,
// TestHardCasesAllPresent and TestModelParity.
// How: presence checks over the units on disk, with the rule and case IDs
// read from doc/memory.md and parity.txt; the verdicts are judged elsewhere.
//
// TestModelParity checks file and line presence, and the file verdicts
// against parity.txt's Go column; TestModelCases judges the verdicts.

package corpus

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/diag"
)

var (
	unitsOnce sync.Once
	unitsList []Unit
	unitsErr  error
)

// diskUnits is Units over the repository, loaded once.
func diskUnits(t *testing.T) []Unit {
	t.Helper()
	unitsOnce.Do(func() { unitsList, unitsErr = Units(repoRoot(t)) })
	if unitsErr != nil {
		t.Fatalf("Units: %v", unitsErr)
	}
	return unitsList
}

// suiteUnits returns the units of one suite, keyed by their name in the
// suite: "E0501.mk", "E0903", "R1_accept_x.mk".
func suiteUnits(t *testing.T, suite string) map[string]*Unit {
	t.Helper()
	out := map[string]*Unit{}
	us := diskUnits(t)
	for i := range us {
		if rest, ok := strings.CutPrefix(us[i].Key, conformanceDir+"/"+suite+"/"); ok {
			out[rest] = &us[i]
		}
	}
	return out
}

// codeExempt are the catalogue codes that have no codes/ unit, with why.
var codeExempt = map[diag.Code]string{
	diag.E0000: "a Go test makes its snapshot (TestE0000_Snapshot, T-14)",
	diag.E0001: "a Go test makes its snapshot (TestE0001_Snapshot, T-14)",
	diag.E0213: "deferred (C import)",
	diag.E0904: "deferred",
	// The corpus judges every unit on the host target, and a hosted bin
	// refuses general-regs-only (E0202), so no codes/ unit can reach
	// E1108; the corpus has no per-unit target.
	diag.E1108: "judged per bin on x86_64-freestanding only; a Go test is its reject test (internal/driver TestGeneralRegsOnlyAtCheck)",
}

// Every core catalogue code has a codes/ unit <code>.mk or <code>/ whose
// verdict rejects with it (the loader has already required its marker). The
// snapshot <code>.json exists to be taken once the unit passes:
// TestSnapshots compares it and, in a gate run, fails on a passing unit's
// missing one; here the counts are logged.
func TestEveryCodeHasATest(t *testing.T) {
	root := repoRoot(t)
	units := suiteUnits(t, "codes")
	status := map[string]Status{}
	for _, u := range fullRun(t).Units {
		status[u.Unit.Key] = u.Status
	}
	var noSnap, passingNoSnap []string
	n := 0
	for _, e := range diag.Catalogue() {
		c := e.Code
		if _, ok := codeExempt[c]; ok {
			continue
		}
		n++
		u := units[c.String()+".mk"]
		if u == nil {
			u = units[c.String()]
		}
		switch {
		case u == nil:
			t.Errorf("%s (%s): no tests/conformance/codes/%s.mk or %s/", c, e.Name, c, c)
		case u.Verdict != VerdictReject || !slices.Contains(u.Codes, c):
			t.Errorf("%s: %s has verdict %s, want reject %s", c, u.Key, verdictText(u), c)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(conformanceDir), "codes", c.String()+".json")); err != nil {
			noSnap = append(noSnap, c.String())
			if u != nil && status[u.Key] == StatusPass {
				passingNoSnap = append(passingNoSnap, c.String())
			}
		}
	}
	t.Logf("%d core codes with a codes/ unit; %d without a snapshot, %d of them passing (TestSnapshots requires those in a gate run): %s",
		n, len(noSnap), len(passingNoSnap), strings.Join(passingNoSnap, " "))
}

// ruleHeading is a "### R10. ..." heading of doc/memory.md.
var ruleHeading = regexp.MustCompile(`^### (R[0-9]+)\. `)

// Every rule R1 to R21 of doc/memory.md (read from its headings) and every
// model-gap rule EV520 to EV536 has at least one rules/<ID>_accept_<slug>
// unit with verdict ok and one rules/<ID>_reject_<slug> unit with a reject
// verdict, each tagged "// rule: <ID>". The prefix is matched with its
// underscore, so R10's files never count for R1.
func TestEveryRuleHasAcceptAndReject(t *testing.T) {
	text, err := readDoc(repoRoot(t), "doc/memory.md")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range splitLines(text) {
		if m := ruleHeading.FindStringSubmatch(l); m != nil {
			ids = append(ids, m[1])
		}
	}
	if len(ids) < 21 {
		t.Fatalf("doc/memory.md: %d rule headings, want R1 to R21", len(ids))
	}
	for n := 520; n <= 536; n++ {
		ids = append(ids, fmt.Sprintf("EV%d", n))
	}
	units := suiteUnits(t, "rules")
	names := slices.Sorted(maps.Keys(units))
	files := 0
	for _, id := range ids {
		for _, kind := range []string{"accept", "reject"} {
			want := VerdictOK
			if kind == "reject" {
				want = VerdictReject
			}
			found := 0
			for _, name := range names {
				if !strings.HasPrefix(name, id+"_"+kind+"_") {
					continue
				}
				u := units[name]
				if u.Verdict != want {
					t.Errorf("%s: verdict %s, want %s for an %s file", u.Key, verdictText(u), want, kind)
				}
				if !slices.Contains(u.Meta.Rule, id) {
					t.Errorf("%s: no \"// rule: %s\" header", u.Key, id)
				}
				found++
			}
			if found == 0 {
				t.Errorf("%s: no tests/conformance/rules/%s_%s_<slug>.mk", id, id, kind)
			}
			files += found
		}
	}
	t.Logf("%d rules, %d accept and reject units (minimum %d)", len(ids), files, 2*len(ids))
}

// hardRow is a "| H05 |" or "| X03 |" row of doc/memory.md's tables of
// hard cases and of programs Rust accepts and Mk rejects.
var hardRow = regexp.MustCompile(`^\| *([HX][0-9]{2}) *\|`)

// hardExempt are the rows of those two tables that have no unit, with why.
var hardExempt = map[string]string{
	"X10": "needs dyn, which is deferred",
}

// caseIDs splits a case or also header value into row IDs: "H07.local" is
// H07, "H05, R10" is H05 and R10.
func caseIDs(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' }) {
		id, _, _ := strings.Cut(f, ".")
		out = append(out, id)
	}
	return out
}

// Every row H01 to H76 of doc/memory.md's hard cases and every row of its
// Rust-accepts table (read from the document) is a unit: a model/ file whose
// case or also header names it, else memory/<ID>.mk or memory/<ID>/. X10 is
// the one exemption. Presence only: the verdicts are judged as units.
func TestHardCasesAllPresent(t *testing.T) {
	text, err := readDoc(repoRoot(t), "doc/memory.md")
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	section := ""
	for _, l := range splitLines(text) {
		if strings.HasPrefix(l, "## ") {
			section = l
			continue
		}
		if !strings.HasPrefix(section, "## 6. ") && !strings.HasPrefix(section, "## 7. ") {
			continue
		}
		if m := hardRow.FindStringSubmatch(l); m != nil {
			rows = append(rows, m[1])
		}
	}
	if len(rows) < 76 {
		t.Fatalf("doc/memory.md's hazard table: %d rows, want at least H01 to H76", len(rows))
	}
	covered := map[string]string{}
	for name, u := range suiteUnits(t, "model") {
		for _, v := range append([]string{u.Meta.Case}, u.Meta.Also...) {
			for _, id := range caseIDs(v) {
				if covered[id] == "" {
					covered[id] = "model/" + name
				}
			}
		}
	}
	for name := range suiteUnits(t, "memory") {
		id := strings.TrimSuffix(name, ".mk")
		if covered[id] == "" {
			covered[id] = "memory/" + name
		}
	}
	for _, id := range rows {
		_, exempt := hardExempt[id]
		switch {
		case exempt && covered[id] != "":
			t.Logf("%s: exempt (%s) but covered by %s", id, hardExempt[id], covered[id])
		case !exempt && covered[id] == "":
			t.Errorf("%s: no model/ unit names it in case or also, and no memory/%s.mk", id, id)
		}
	}
	for id := range hardExempt {
		if !slices.Contains(rows, id) {
			t.Errorf("exemption %s names no row of doc/memory.md's hazard table", id)
		}
	}
	t.Logf("%d hard-case rows, %d exempt", len(rows), len(hardExempt))
}

// parityVerdict spells a unit's verdict as parity.txt does: ok, parse, or
// reject:E0501,E0505 with the codes sorted.
func parityVerdict(u *Unit) string {
	switch u.Verdict {
	case VerdictOK:
		return "ok"
	case VerdictParse:
		return "parse"
	}
	return "reject:" + strings.ReplaceAll(codeList(u.Codes), " ", ",")
}

// normalParity sorts the codes of a parity.txt verdict; "" when malformed.
func normalParity(v string) string {
	if v == "ok" || v == "parse" {
		return v
	}
	list, ok := strings.CutPrefix(v, "reject:")
	if !ok || list == "" {
		return ""
	}
	var cs []diag.Code
	for _, s := range strings.Split(list, ",") {
		c, ok := diag.ParseCode(s)
		if !ok || !c.IsKnown() || slices.Contains(cs, c) {
			return ""
		}
		cs = append(cs, c)
	}
	slices.Sort(cs)
	return "reject:" + strings.ReplaceAll(codeList(cs), " ", ",")
}

// modelCases is the number of model cases (59 R cases, 87 H cases).
const modelCases = 146

// The presence part: parity.txt has exactly 146 well-formed lines with
// distinct case IDs; every line has its model/<case ID>.mk (or directory)
// whose case header names it, and every model/ unit has its line; each
// file's verdict equals the line's Go verdict; a model and Go verdict that
// differ carry a reason other than "-". Comparing the Go verdicts with what
// the compiler judges is TestModelCases' part.
func TestModelParity(t *testing.T) {
	root := repoRoot(t)
	text, err := readText(filepath.Join(root, filepath.FromSlash(conformanceDir), "model", "parity.txt"))
	if err != nil {
		t.Fatal(err)
	}
	units := map[string]*Unit{} // by case header
	for name, u := range suiteUnits(t, "model") {
		if u.Meta.Case == "" {
			t.Errorf("model/%s: no case header", name)
			continue
		}
		if base := strings.TrimSuffix(path.Base(u.Key), ".mk"); base != u.Meta.Case {
			t.Errorf("model/%s: case %s, want the file named after its case", name, u.Meta.Case)
		}
		units[u.Meta.Case] = u
	}
	lines := 0
	seen := map[string]bool{}
	for i, l := range splitLines(text) {
		if l == "" {
			continue
		}
		lines++
		at := fmt.Sprintf("parity.txt:%d", i+1)
		f := strings.Fields(l)
		if len(f) < 4 {
			t.Errorf("%s: %q, want <case ID> <model verdict> <Go verdict> <reason>", at, l)
			continue
		}
		id, model, gov, reason := f[0], normalParity(f[1]), normalParity(f[2]), strings.Join(f[3:], " ")
		if model == "" || gov == "" {
			t.Errorf("%s: verdicts %q %q, want ok, parse or reject:Exxxx[,Exxxx]", at, f[1], f[2])
			continue
		}
		if seen[id] {
			t.Errorf("%s: case %s listed twice", at, id)
		}
		seen[id] = true
		if model != gov && reason == "-" {
			t.Errorf("%s: %s: model %s and Go %s differ with reason -", at, id, f[1], f[2])
		}
		u := units[id]
		if u == nil {
			t.Errorf("%s: case %s has no tests/conformance/model/%s.mk", at, id, id)
			continue
		}
		if pv := parityVerdict(u); pv != gov {
			t.Errorf("%s: %s: the file's verdict is %s, parity.txt's Go verdict %s", at, id, pv, f[2])
		}
	}
	for _, id := range slices.Sorted(maps.Keys(units)) {
		if !seen[id] {
			t.Errorf("%s: no parity.txt line", units[id].Key)
		}
	}
	if lines != modelCases {
		t.Errorf("parity.txt: %d lines, want %d", lines, modelCases)
	}
	t.Logf("parity.txt: %d lines, %d model units", lines, len(units))
}
