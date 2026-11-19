// Why: a std set that may gain members in a compatible release must be written
// `open`, so a user's match on it has to cover the members it may gain.
// What: TestStdDefects_OpenSetsAreWrittenOpen, TestNamedSetsOfAnErrorAreOpen,
// TestOpenSetsNameOnlyStd and TestOpenSetsDeclareAMember.
// How: scans std's sources: every module's Error union, each error set it names
// (and each set those name), and each enum doc/reference.md marks "may gain
// members" is written `open`, no other declaration is, and the old doc line
// `/// May gain members.`, which the word replaces, is left nowhere.

package std

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// openDocLine is the old doc line, replaced by the word open.
const openDocLine = "/// May gain members."

// openMarkComment is the reference's mark of open enums: "// Kind may gain
// members", "// Curve and HashKind may gain members".
var openMarkComment = regexp.MustCompile(`^// ([A-Z][A-Za-z0-9]*(?:(?:, | and )[A-Z][A-Za-z0-9]*)*) may gain members$`)

// openDeclHead is a top-level exported declaration's head: the word open,
// its kind and its name.
var openDeclHead = regexp.MustCompile(`^export (open )?(type|enum|error|struct|const|var|impl|interface|trait)\s+([A-Z][A-Za-z0-9]*)`)

// openUnion is a named union of error sets: `export type Parse = A | b.B`.
// The module's own Error is one of them.
var openUnion = regexp.MustCompile(`^export (?:open )?type ([A-Z][A-Za-z0-9]*) = (.*\|.*)$`)

// openDecl is one exported declaration of std's source.
type openDecl struct {
	key    string // "std.fs.Kind"
	file   string
	marked bool // it is written open
}

// openScan is what openScanFile gathers over std's sources.
type openScan struct {
	decls  map[string]openDecl
	unions map[string][]string // every named union's members, by its key
	stray  []string            // doc lines holding openDocLine, which the word open retired
}

func newOpenScan() *openScan {
	return &openScan{decls: map[string]openDecl{}, unions: map[string][]string{}}
}

// openScanFile adds the exported declarations of one source file p of
// module mod ("std.fs") to sc, with the members of every named union.
func openScanFile(sc *openScan, mod, p, text string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var doc []string
	docAt := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "///") {
			if doc == nil {
				docAt = i
			}
			doc = append(doc, strings.TrimRight(l, " "))
			continue
		}
		for _, dl := range doc {
			if dl == openDocLine {
				sc.stray = append(sc.stray, p+":"+strconv.Itoa(docAt+1))
			}
		}
		if m := openDeclHead.FindStringSubmatch(l); m != nil {
			k := mod + "." + m[3]
			sc.decls[k] = openDecl{key: k, file: p, marked: m[1] != ""}
			if u := openUnion.FindStringSubmatch(l); u != nil {
				for _, mem := range strings.Split(u[2], "|") {
					sc.unions[k] = append(sc.unions[k], strings.TrimSpace(mem))
				}
			}
		}
		doc = nil
	}
}

// openStdDecls reads every non-test .mk file under std.
func openStdDecls(t *testing.T) *openScan {
	t.Helper()
	sc := newOpenScan()
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".mk-cache" {
				return filepath.SkipDir
			}
			return nil
		}
		p = filepath.ToSlash(p)
		if !strings.HasSuffix(p, ".mk") || !strings.Contains(p, "/") || strings.HasPrefix(filepath.Base(p), "tests_") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		openScanFile(sc, "std."+strings.ReplaceAll(p[:strings.LastIndex(p, "/")], "/", "."), p, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// openReferenceEnums returns the keys of the enums the reference marks open,
// each in the module of the "// std.x" section that marks it.
func openReferenceEnums(t *testing.T) []string {
	t.Helper()
	var keys []string
	sect := ""
	for _, l := range referenceAPIBlock(t) {
		if m := sectionComment.FindStringSubmatch(l); m != nil {
			sect = m[1]
		}
		m := openMarkComment.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if sect == "" {
			t.Errorf("the library surface marks %q outside a module's section", m[1])
			continue
		}
		for _, part := range strings.Split(m[1], ", ") {
			for _, name := range strings.Split(part, " and ") {
				keys = append(keys, sect+"."+name)
			}
		}
	}
	return keys
}

// openWanted is the set of open declarations, each with why it is open:
// every module's Error union, every named union of the same module an
// open union names (followed to the end, so a set named by a named set is
// open too), and each enum key in enums.
func openWanted(sc *openScan, enums []string) map[string]string {
	want := map[string]string{}
	var queue []string
	for k := range sc.unions {
		if strings.HasSuffix(k, ".Error") {
			want[k] = "a module's Error union"
			queue = append(queue, k)
		}
	}
	sort.Strings(queue)
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		mod := k[:strings.LastIndex(k, ".")]
		for _, mem := range sc.unions[k] {
			// A member another module qualifies is that module's own set,
			// open or closed as such; an unqualified one is an error set
			// of this module when it is a union, not a single error.
			if strings.Contains(mem, ".") {
				continue
			}
			mk := mod + "." + mem
			if _, ok := sc.unions[mk]; !ok {
				continue
			}
			if _, seen := want[mk]; seen {
				continue
			}
			want[mk] = "an error set " + k + " names"
			queue = append(queue, mk)
		}
	}
	for _, k := range enums {
		want[k] = "marked open in the library surface"
	}
	return want
}

// openProblems is every way sc departs from want: an open declaration that
// is missing or not written open, a closed one written open, and a doc line
// holding openDocLine that is left. Sorted.
func openProblems(sc *openScan, want map[string]string) []string {
	var out []string
	for k, why := range want {
		d, ok := sc.decls[k]
		if !ok {
			out = append(out, k+" ("+why+") is declared nowhere in std")
			continue
		}
		if !d.marked {
			out = append(out, k+" ("+why+", "+d.file+") is not written open")
		}
	}
	for k, d := range sc.decls {
		if d.marked && want[k] == "" {
			out = append(out, k+" ("+d.file+") is written open but is no open set: every other std declaration is closed")
		}
	}
	for _, s := range sc.stray {
		out = append(out, s+": "+strconv.Quote(openDocLine)+" is the old mark, which the word open replaced")
	}
	sort.Strings(out)
	return out
}

// TestStdDefects_OpenSetsAreWrittenOpen: the set of std declarations
// written `open` is exactly the open sets: each module's Error union, each
// error set it names, and each enum the reference marks.
func TestStdDefects_OpenSetsAreWrittenOpen(t *testing.T) {
	t.Parallel()
	sc := openStdDecls(t)
	enums := openReferenceEnums(t)
	if len(enums) == 0 {
		t.Fatal("the library surface marks no enum open; seven are open")
	}
	want := openWanted(sc, enums)
	// The prelude's Arch and Os grow with the targets, and the reference
	// says so in a sentence of its own, outside a module's section. They
	// are declared, and so written open, in std.target; std.core's
	// `type Arch = target.Arch` and `type Os = target.Os` are aliases of
	// them, which the word open may not mark (E0706).
	for _, k := range []string{"std.target.Arch", "std.target.Os"} {
		want[k] = "a target enum"
	}
	for _, p := range openProblems(sc, want) {
		t.Error(p)
	}
	errs := 0
	for k := range sc.unions {
		if strings.HasSuffix(k, ".Error") {
			errs++
		}
	}
	if errs < 20 {
		t.Errorf("found %d Error unions; std has more than 20", errs)
	}
}

// TestNamedSetsOfAnErrorAreOpen: a union an Error names, and a
// union that one names, must be written open, and when they are they are
// not reported as extra; a union no Error names stays closed, and the old
// doc line is reported wherever it is left.
func TestNamedSetsOfAnErrorAreOpen(t *testing.T) {
	t.Parallel()
	const fixture = `/// The module's errors.
export open type Error = Parse | Io | other.Error

/// Parse failures.
export type Parse = Syntax | Depth

export type Depth = TooDeep | TooWide

/// Not named by Error.
export type Loose = Syntax | Io

export error Syntax
export error Io
`
	sc := newOpenScan()
	openScanFile(sc, "std.fx", "fx/fx.mk", fixture)
	got := openProblems(sc, openWanted(sc, nil))
	want := []string{
		`std.fx.Depth (an error set std.fx.Parse names, fx/fx.mk) is not written open`,
		`std.fx.Parse (an error set std.fx.Error names, fx/fx.mk) is not written open`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("unmarked fixture:\n got %q\nwant %q", got, want)
	}
	marked := strings.Replace(fixture, "export type Parse", "export open type Parse", 1)
	marked = strings.Replace(marked, "export type Depth", "export open type Depth", 1)
	sc = newOpenScan()
	openScanFile(sc, "std.fx", "fx/fx.mk", marked)
	if got := openProblems(sc, openWanted(sc, nil)); len(got) != 0 {
		t.Errorf("marked fixture: %q", got)
	}
	loose := strings.Replace(marked, "export type Loose", "export open type Loose", 1)
	sc = newOpenScan()
	openScanFile(sc, "std.fx", "fx/fx.mk", loose)
	got = openProblems(sc, openWanted(sc, nil))
	if len(got) != 1 || !strings.HasPrefix(got[0], "std.fx.Loose (fx/fx.mk) is written open") {
		t.Errorf("a closed union marked: %q", got)
	}
	left := strings.Replace(marked, "/// Not named by Error.\n", "/// Not named by Error.\n/// May gain members.\n", 1)
	sc = newOpenScan()
	openScanFile(sc, "std.fx", "fx/fx.mk", left)
	got = openProblems(sc, openWanted(sc, nil))
	if len(got) != 1 || !strings.Contains(got[0], "the word open replaced") {
		t.Errorf("the open-enum doc line left: %q", got)
	}
}

// TestOpenSetsNameOnlyStd: the word open is written only in the
// package that declares every member of the set, which the compiler checks
// (E0706 reason open-foreign, so TestStdChecksClean holds it for std's
// sources); this scan says the same of the text: every member an open std
// set names is an error of its own module or of a std module it names by
// its last segment.
func TestOpenSetsNameOnlyStd(t *testing.T) {
	sc := openStdDecls(t)
	last := map[string]bool{}
	for k := range sc.decls {
		mod := k[:strings.LastIndex(k, ".")]
		last[mod[strings.LastIndex(mod, ".")+1:]] = true
	}
	n := 0
	for k, mems := range sc.unions {
		if !sc.decls[k].marked {
			continue
		}
		n++
		mod := k[:strings.LastIndex(k, ".")]
		for _, m := range mems {
			q, name, qualified := strings.Cut(m, ".")
			if !qualified {
				if _, ok := sc.decls[mod+"."+m]; !ok {
					if _, ok := sc.decls["std.core."+m]; ok {
						continue // the prelude's, named unqualified
					}
					t.Errorf("%s (%s) is written open and names %s, no error of %s", k, sc.decls[k].file, m, mod)
				}
				continue
			}
			if !last[q] || name == "" {
				t.Errorf("%s (%s) is written open and names %s, of no std module", k, sc.decls[k].file, m)
			}
		}
	}
	if n < 20 {
		t.Errorf("found %d error sets written open in std, want at least 20", n)
	}
}

// TestOpenSetsDeclareAMember: the word open is written only in a
// module that declares one of the set's members, which the compiler checks (E0706
// reason open-away, so TestStdChecksClean holds it for std's sources);
// this scan says the same of the text: every error set std writes open
// names an error of its own module.
func TestOpenSetsDeclareAMember(t *testing.T) {
	sc := openStdDecls(t)
	n := 0
	for k, mems := range sc.unions {
		if !sc.decls[k].marked {
			continue
		}
		n++
		mod := k[:strings.LastIndex(k, ".")]
		own := false
		for _, m := range mems {
			if !strings.Contains(m, ".") {
				_, ok := sc.decls[mod+"."+m]
				own = own || ok
			}
		}
		if !own {
			t.Errorf("%s (%s) is written open and names no error of %s", k, sc.decls[k].file, mod)
		}
	}
	if n < 20 {
		t.Errorf("found %d error sets written open in std, want at least 20", n)
	}
}
