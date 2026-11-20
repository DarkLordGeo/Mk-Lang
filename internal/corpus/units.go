// Why: the conformance suites under tests/conformance are read strictly, so
// a misplaced file or a malformed header is an error, not a unit that
// silently checks nothing.
//
// What: Suites (the suite directories and their prelude default), Units,
// which loads every conformance unit, and makeUnit.
//
// How: suites are read in byte order of paths; anything else directly
// under tests/conformance is an error. A unit is a file or a directory; the
// verdict is line 1 of its first file, header lines ("// needs:", "// rule:",
// ...) follow, and markers sit on the lines they expect. Dot-names (a
// build cache) are skipped.

package corpus

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mk/internal/diag"
	"mk/internal/driver"
)

// Suites are the directories of tests/conformance, in byte order, with
// whether the test prelude is on by default.
var Suites = []struct {
	Name    string
	Prelude bool
}{
	{"codes", false},
	{"diff", false},
	{"memory", true},
	{"model", true},
	{"rules", true},
}

// conformanceDir is the units' root, repository-relative.
const conformanceDir = "tests/conformance"

// headerLine is a "// key: value" header line.
var headerLine = regexp.MustCompile(`^// ([a-z]+): (.*)$`)

// ruleID is the form of an ID a "// rule:" or "// also:" header names: R,
// H or X and digits, or one to four capitals, an optional hyphenated
// infix, an optional hyphen and digits (R10, H05, EV533, MMA-031, S-G208).
var ruleID = regexp.MustCompile(`^([RHX][0-9]+|[A-Z]{1,4}(-[A-Z]{1,2})?-?[0-9]+)$`)

// phaseByName maps a "// needs:" value to its phase.
var phaseByName = map[string]diag.Phase{
	"parse": diag.PhaseParse, "imports": diag.PhaseImports, "names": diag.PhaseNames,
	"types": diag.PhaseTypes, "flow": diag.PhaseFlow,
}

// Units loads every unit of the suites under root/tests/conformance, in
// byte order of paths: a .mk file is a file unit, a directory a directory
// unit whose verdict is in main.mk; every other file is data. Any other
// directory or .mk file directly under tests/conformance is an error, as is
// a malformed unit.
func Units(root string) ([]Unit, error) {
	base := filepath.Join(root, filepath.FromSlash(conformanceDir))
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if hidden(e.Name()) {
			continue
		}
		known := slices.ContainsFunc(Suites, func(s struct {
			Name    string
			Prelude bool
		}) bool {
			return s.Name == e.Name()
		})
		if (e.IsDir() && !known) || (!e.IsDir() && strings.HasSuffix(e.Name(), ".mk")) {
			return nil, fmt.Errorf("%s/%s: not a suite (%s)", conformanceDir, e.Name(), suiteNames())
		}
	}
	var out []Unit
	for _, s := range Suites {
		dir := filepath.Join(base, s.Name)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range ents { // ReadDir sorts by name
			if hidden(e.Name()) {
				continue // .mk-cache and other tool state, gitignored
			}
			rel := conformanceDir + "/" + s.Name + "/" + e.Name()
			var u Unit
			switch {
			case e.IsDir():
				u, err = loadDirUnit(filepath.Join(dir, e.Name()), rel, s.Name, s.Prelude)
			case strings.HasSuffix(e.Name(), ".mk"):
				var text string
				text, err = readText(filepath.Join(dir, e.Name()))
				if err == nil {
					u, err = makeUnit(rel, s.Name, s.Prelude, []UnitFile{{Path: e.Name(), Text: text}})
				}
			default:
				continue // data: snapshots, goldens, parity.txt
			}
			if err != nil {
				return nil, err
			}
			out = append(out, u)
		}
	}
	return out, nil
}

func codeList(cs []diag.Code) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.String())
	}
	return strings.Join(n, " ")
}

func suiteNames() string {
	var n []string
	for _, s := range Suites {
		n = append(n, s.Name)
	}
	return strings.Join(n, ", ")
}

func readText(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), nil
}

// loadDirUnit reads every .mk file under dir: main.mk first, then the rest in
// byte order of their unit-relative paths.
func loadDirUnit(dir, rel, suite string, prelude bool) (Unit, error) {
	var files []UnitFile
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		r, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		text, err := readText(p)
		if err != nil {
			return err
		}
		files = append(files, UnitFile{Path: filepath.ToSlash(r), Text: text})
		return nil
	})
	if err != nil {
		return Unit{}, err
	}
	slices.SortFunc(files, func(a, b UnitFile) int {
		if (a.Path == "main.mk") != (b.Path == "main.mk") {
			if a.Path == "main.mk" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(files) == 0 || files[0].Path != "main.mk" {
		return Unit{}, fmt.Errorf("%s: a directory unit needs main.mk", rel)
	}
	return makeUnit(rel, suite, prelude, files)
}

// makeUnit reads the verdict and header of files[0] and the markers of
// every file.
func makeUnit(rel, suite string, prelude bool, files []UnitFile) (Unit, error) {
	u := Unit{Key: rel, Name: rel, Files: files, Meta: Meta{Prelude: prelude}}
	first := files[0]
	at := func(f UnitFile, line int) string {
		if len(files) == 1 && path.Base(rel) == f.Path {
			return fmt.Sprintf("%s:%d", rel, line)
		}
		return fmt.Sprintf("%s/%s:%d", rel, f.Path, line)
	}
	for _, f := range files {
		// corpus-3: the test prelude's file name is the compiler's; a unit file
		// of that name would replace it, and its diagnostics would count as
		// outside the unit (Unit's invariant, judge rule 2).
		if f.Path == PreludeFile {
			return Unit{}, fmt.Errorf("%s: %s is reserved for the test prelude; rename the file", at(f, 1), PreludeFile)
		}
	}
	lines := splitLines(first.Text)
	if len(lines) == 0 {
		return Unit{}, fmt.Errorf("%s: no verdict line", at(first, 1))
	}
	v, codes, err := parseVerdict(lines[0])
	if err != nil {
		return Unit{}, fmt.Errorf("%s: %v", at(first, 1), err)
	}
	u.Verdict, u.Codes = v, codes
	preludeSet := false
	for i := 1; i < len(lines); i++ {
		m := headerLine.FindStringSubmatch(lines[i])
		if m == nil {
			break
		}
		key, val := m[1], strings.TrimSpace(m[2])
		where := at(first, i+1)
		switch key {
		case "case":
			if suite != "model" {
				return Unit{}, fmt.Errorf("%s: case outside model/", where)
			}
			u.Meta.Case = val
		case "also":
			u.Meta.Also = append(u.Meta.Also, val)
		case "rule":
			if !ruleID.MatchString(val) {
				return Unit{}, fmt.Errorf("%s: rule %q is not a design or checklist ID", where, val)
			}
			u.Meta.Rule = append(u.Meta.Rule, val)
		case "needs":
			p, ok := phaseByName[val]
			if !ok {
				return Unit{}, fmt.Errorf("%s: needs %q is not a phase (parse, imports, names, types, flow)", where, val)
			}
			if v != VerdictReject {
				return Unit{}, fmt.Errorf("%s: needs on a %s unit", where, v)
			}
			if !slices.ContainsFunc(codes, func(c diag.Code) bool { return EmittedIn(c, p) }) {
				// corpus-2: a header the judge would not honour.
				return Unit{}, fmt.Errorf("%s: needs %s, but no pass emits %s in %s", where, val, codeList(codes), val)
			}
			u.Meta.Needs = p
		case "prelude":
			switch val {
			case "yes":
				u.Meta.Prelude = true
			case "no":
				u.Meta.Prelude = false
			default:
				return Unit{}, fmt.Errorf("%s: prelude %q, want yes or no", where, val)
			}
			if preludeSet {
				return Unit{}, fmt.Errorf("%s: prelude given twice", where)
			}
			preludeSet = true
		case "note":
			u.Meta.Note = strings.TrimSpace(u.Meta.Note + " " + val)
		case "host":
			if _, _, ok := driver.TargetNamed(val); !ok {
				return Unit{}, fmt.Errorf("%s: host %q is not a target name", where, val)
			}
			if u.Meta.Host != "" {
				return Unit{}, fmt.Errorf("%s: host given twice", where)
			}
			u.Meta.Host = val
		case "arch":
			if val != "x86_64" && val != "aarch64" {
				return Unit{}, fmt.Errorf("%s: arch %q, want x86_64 or aarch64", where, val)
			}
			if u.Meta.Arch != "" {
				return Unit{}, fmt.Errorf("%s: arch given twice", where)
			}
			u.Meta.Arch = val
		default:
			return Unit{}, fmt.Errorf("%s: unknown header key %q", where, key)
		}
	}
	for _, f := range files {
		for i, line := range splitLines(f.Text) {
			cs, err := parseMarker(line)
			if err != nil {
				return Unit{}, fmt.Errorf("%s: %v", at(f, i+1), err)
			}
			for _, c := range cs {
				if v != VerdictReject {
					return Unit{}, fmt.Errorf("%s: marker %s in a unit whose verdict is %s", at(f, i+1), c, v)
				}
				if !slices.Contains(codes, c) {
					return Unit{}, fmt.Errorf("%s: marker %s is not in the verdict's codes", at(f, i+1), c)
				}
				u.Expected = append(u.Expected, UnitMarker{File: f.Path, Line: i + 1, Code: c})
			}
		}
	}
	u.Expected = sortMarkers(u.Expected)
	for _, c := range codes {
		if !slices.ContainsFunc(u.Expected, func(m UnitMarker) bool { return m.Code == c }) {
			return Unit{}, fmt.Errorf("%s: verdict code %s has no marker in the unit", at(first, 1), c)
		}
	}
	return u, nil
}

// hidden reports a dot-name: a build cache (.mk-cache, which mk writes next
// to any root it checks or builds) or other tool state, never a suite or a
// unit.
func hidden(name string) bool { return strings.HasPrefix(name, ".") }
