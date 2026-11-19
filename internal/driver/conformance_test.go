// Why: the driver's corpus entry points must handle every conformance unit
// before mk corpus judges them in full.
// What: a driver-side smoke run over tests/conformance
// (TestConformanceUnitsThroughNames) and pins of units with known pitfalls.
// How: every unit goes through CheckUnit without an ICE, and every parse-,
// imports- or names-phase reject unit (E01xx, E02xx, E12xx) gives exactly
// its marked codes at its marked lines against a std that declares the
// prelude. A .mk file is a unit; a directory is one with its verdict in
// main.mk. The judging proper is mk corpus's.

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/diag"
)

var (
	markerRE = regexp.MustCompile(`// <- (E[0-9]{4})`)
	headerRE = regexp.MustCompile(`^// ([a-z]+): (.*)$`)
)

// confUnit is one conformance unit read from disk.
type confUnit struct {
	name    string // "codes/E0205"
	files   []UnitFile
	verdict string // "ok", "parse", "reject E0201 E0202"
	prelude bool
	needs   diag.Phase // the needs header, PhaseParse when absent
}

func readConfUnits(t *testing.T, root string) []confUnit {
	t.Helper()
	var units []confUnit
	for _, suite := range []string{"codes", "diff", "memory", "model", "rules"} {
		ents, err := os.ReadDir(filepath.Join(root, suite))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				continue // .mk-cache and other tool state, as corpus.Units skips them
			}
			u := confUnit{name: suite + "/" + strings.TrimSuffix(e.Name(), ".mk"), needs: diag.PhaseParse}
			switch {
			case e.IsDir():
				dir := filepath.Join(root, suite, e.Name())
				err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
						return err
					}
					rel, _ := filepath.Rel(dir, p)
					u.files = append(u.files, UnitFile{filepath.ToSlash(rel), readLF(t, p)})
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				slices.SortFunc(u.files, func(a, b UnitFile) int { return strings.Compare(a.Path, b.Path) })
			case strings.HasSuffix(e.Name(), ".mk"):
				u.files = []UnitFile{{e.Name(), readLF(t, filepath.Join(root, suite, e.Name()))}}
			default:
				continue
			}
			head := u.files[0].Text
			for _, f := range u.files {
				if f.Path == "main.mk" {
					head = f.Text
				}
			}
			lines := strings.Split(head, "\n")
			u.verdict = strings.TrimSpace(strings.TrimPrefix(lines[0], "// verdict:"))
			u.prelude = suite == "memory" || suite == "model" || suite == "rules"
			for _, l := range lines[1:] {
				m := headerRE.FindStringSubmatch(l)
				if m == nil {
					break
				}
				switch m[1] {
				case "prelude":
					u.prelude = m[2] == "yes"
				case "needs":
					u.needs = phaseNamed(t, u.name, m[2])
				}
			}
			units = append(units, u)
		}
	}
	return units
}

func phaseNamed(t *testing.T, unit, name string) diag.Phase {
	for p := diag.PhaseParse; p.IsValid(); p++ {
		if p.String() == name {
			return p
		}
	}
	t.Fatalf("%s: needs %q is no phase", unit, name)
	return 0
}

func readLF(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// early reports whether every code of a reject verdict is one a parse,
// imports or names pass emits (and none of the types-phase ones of those
// groups: E0208 for imports and E0209).
func early(verdict string) bool {
	codes := strings.Fields(strings.TrimPrefix(verdict, "reject"))
	if !strings.HasPrefix(verdict, "reject") || len(codes) == 0 {
		return false
	}
	for _, c := range codes {
		if !strings.HasPrefix(c, "E01") && !strings.HasPrefix(c, "E02") && !strings.HasPrefix(c, "E12") ||
			c == "E0208" || c == "E0209" {
			return false
		}
	}
	return true
}

// typesGroup are early-code reject units whose code the checker emits, in
// the types group, after the names stage: member lookups (E0203, E0207 on a
// member, E0204 on a field or a destructure), E1202 and `.await` (E1204).
// They carry no `// needs: types` header; a unit whose needs header names a
// phase past Through is skipped by that header.
var typesGroup = map[string]bool{
	"codes/E0203": true, "codes/E0204_field": true, "codes/E0207_member": true, "codes/E1202": true,
	"codes/E1204_await": true, "memory/X09": true, "rules/EV533_reject_other_module": true,
	// check reports an index form on an iterator, and the iterator is
	// std's str.Lines, which this harness's fake std lacks.
	"codes/E0101_for_index_iterator": true,
	// A member lookup on std's Io (no deadline or Cancel of its own) whose
	// units import std.time; this harness's fake std has neither the real Io
	// nor std.time (E0202), so mk corpus judges them against the real std.
	"rules/D491_reject_no_deadline_in_io": true, "rules/D649_reject_mask_has_no_cancel": true,
}

// knownOther are early-code reject units that fail for a reason outside
// the driver. An entry that passes is an error, so the list only shrinks.
// It is empty.
var knownOther = map[string]string{}

// knownICE are units whose check reaches lower and gives an E0000 for a
// reason outside the driver. The value is a fragment of the E0000's
// message and the package responsible. An entry whose unit no longer gives
// that E0000 is an error, so the list only shrinks. Any other E0000 fails
// the test like every other unit's.
var knownICE = map[string][2]string{
	// The const's initializer calls EvalArena(), which the evaluator
	// serves through std.core's MakeEvalArena; the stand-in std has none.
	// TestE1002_ConstPointeeIsReadOnly and mk corpus --all judge the unit
	// with the real std.
	"codes/E1002_const_pointee": {"EvalArena() with no Config.EvalArena function", "the stand-in std has no MakeEvalArena"},
}

func TestConformanceUnitsThroughNames(t *testing.T) {
	units := readConfUnits(t, "../../tests/conformance")
	if len(units) < 100 {
		t.Fatalf("only %d units found", len(units))
	}
	std := fakeStdFiles()
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, std)})
	if err != nil {
		t.Fatal(err)
	}
	checkListsName(t, units)
	judged := 0
	for _, u := range units {
		in := UnitInput{Name: "tests/conformance/" + u.name, Files: slices.Clone(u.files), ParseOnly: u.verdict == "parse"}
		if u.prelude && !in.ParseOnly {
			in.Prelude = "prelude.mk"
			in.Files = append(in.Files, UnitFile{"prelude.mk", std["corpus/testprelude.mk"]})
			slices.SortFunc(in.Files, func(a, b UnitFile) int { return strings.Compare(a.Path, b.Path) })
		}
		out := c.CheckUnit(in)
		var got []string
		ki, isKnown := knownICE[u.name]
		sawKnown := false
		for _, e := range out.Report.Diags {
			if e.Diag.Code.String() == "E0000" {
				if isKnown && strings.Contains(e.Diag.Message+fact(e.Diag, "what"), ki[0]) {
					sawKnown = true
					t.Logf("%s: known E0000, %s", u.name, ki[1])
					continue
				}
				t.Errorf("%s: E0000\n%s", u.name, out.Report.Text(out.Sources))
			}
			lc := out.Sources.LineCol(e.Diag.At.StartPos())
			got = append(got, out.Paths[e.Diag.At.File]+":"+strconv.Itoa(int(lc.Line))+":"+e.Diag.Code.String())
		}
		if isKnown && !sawKnown {
			t.Errorf("%s gives no E0000 %q now: remove it from knownICE", u.name, ki[0])
		}
		if u.verdict == "parse" && len(got) > 0 {
			t.Errorf("%s: verdict parse, got %q", u.name, got)
		}
		if !early(u.verdict) || typesGroup[u.name] || u.needs > c.Through() {
			continue
		}
		judged++
		var want []string
		for _, f := range u.files {
			for i, l := range strings.Split(f.Text, "\n") {
				for _, m := range markerRE.FindAllStringSubmatch(l, -1) {
					want = append(want, f.Path+":"+strconv.Itoa(i+1)+":"+m[1])
				}
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		got, want = slices.Compact(got), slices.Compact(want)
		if why, ok := knownOther[u.name]; ok {
			if slices.Equal(got, want) {
				t.Errorf("%s passes now: remove it from knownOther", u.name)
			} else {
				t.Logf("%s: known failure, %s", u.name, why)
			}
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s (%s):\n got %q\nwant %q\n%s", u.name, u.verdict, got, want, out.Report.Text(out.Sources))
		}
	}
	t.Logf("%d units, %d early reject units judged", len(units), judged)
}

// checkListsName fails when an exception list names a unit that is not an
// early-code reject unit on disk (renamed, removed or re-verdicted), since
// such an entry would excuse nothing and hide nothing, and would never be
// reported as passing.
func checkListsName(t *testing.T, units []confUnit) {
	t.Helper()
	byName := map[string]confUnit{}
	for _, u := range units {
		byName[u.name] = u
	}
	check := func(list, name string) {
		u, ok := byName[name]
		switch {
		case !ok:
			t.Errorf("%s names %s, which is no unit of tests/conformance", list, name)
		case !early(u.verdict):
			t.Errorf("%s names %s, whose verdict %q is not an early-code reject", list, name, u.verdict)
		}
	}
	for name := range knownOther {
		check("knownOther", name)
	}
	for name := range typesGroup {
		check("typesGroup", name)
	}
	// knownICE may name any unit (its E0000 is not about the verdict), but
	// it must name one that exists.
	for name := range knownICE {
		if _, ok := byName[name]; !ok {
			t.Errorf("knownICE names %s, which is no unit of tests/conformance", name)
		}
	}
}

// TestFormerKnownICEUnitsHaveNoE0000 pins the units that once gave an
// E0000 and left knownICE: each goes through CheckUnit against the
// stand-in std with no E0000, and none is back in knownICE.
func TestFormerKnownICEUnitsHaveNoE0000(t *testing.T) {
	former := []string{"model/H34", "model/R12.Bad", "model/X.FnCover"}
	byName := map[string]confUnit{}
	for _, u := range readConfUnits(t, "../../tests/conformance") {
		byName[u.name] = u
	}
	std := fakeStdFiles()
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, std)})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range former {
		u, ok := byName[name]
		if !ok {
			t.Errorf("%s is no unit of tests/conformance", name)
			continue
		}
		if _, back := knownICE[name]; back {
			t.Errorf("%s is in knownICE again", name)
		}
		in := UnitInput{Name: "tests/conformance/" + u.name, Files: slices.Clone(u.files), ParseOnly: u.verdict == "parse"}
		if u.prelude && !in.ParseOnly {
			in.Prelude = "prelude.mk"
			in.Files = append(in.Files, UnitFile{"prelude.mk", std["corpus/testprelude.mk"]})
			slices.SortFunc(in.Files, func(a, b UnitFile) int { return strings.Compare(a.Path, b.Path) })
		}
		out := c.CheckUnit(in)
		for _, e := range out.Report.Diags {
			if e.Diag.Code.String() == "E0000" {
				t.Errorf("%s: E0000\n%s", name, out.Report.Text(out.Sources))
				break
			}
		}
	}
}

// TestE1206_CStyleForUnitExact pins the unit that left knownOther:
// codes/E1206_c_for gives exactly one E1206 at its marker (main.mk line 5,
// the first `;`) and no E0101 beside it.
func TestE1206_CStyleForUnitExact(t *testing.T) {
	var unit *confUnit
	for _, u := range readConfUnits(t, "../../tests/conformance") {
		if u.name == "codes/E1206_c_for" {
			unit = &u
			break
		}
	}
	if unit == nil {
		t.Fatal("tests/conformance/codes/E1206_c_for.mk not found")
	}
	if knownOther[unit.name] != "" {
		t.Fatalf("%s is back in knownOther", unit.name)
	}
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, fakeStdFiles())})
	if err != nil {
		t.Fatal(err)
	}
	out := c.CheckUnit(UnitInput{Name: "tests/conformance/" + unit.name, Files: slices.Clone(unit.files)})
	var got []string
	for _, e := range out.Report.Diags {
		lc := out.Sources.LineCol(e.Diag.At.StartPos())
		got = append(got, out.Paths[e.Diag.At.File]+":"+strconv.Itoa(int(lc.Line))+":"+e.Diag.Code.String())
	}
	want := []string{"E1206_c_for.mk:5:E1206"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q\n%s", got, want, out.Report.Text(out.Sources))
	}
}
