// Why: every example in doc/ states which names-phase errors it draws; the
// resolver must agree with each one.
// What: TestCorpusDocBlocks: every ok and reject block of doc/**/*.md
// (the std module pages, which need the real std, left to mk corpus),
// resolved as its own module, emits exactly the names-phase codes its
// markers name, and no unmarked E0208.
// How: the reference's test prelude is a file of each block's module, with
// a stand-in for the std prelude. Resolve reports no E0609: a capture's
// E0609 is the checker's.

package resolve

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mk/internal/diag"
)

// docBlock is one fenced mk block of a document under doc/.
type docBlock struct {
	name    string
	verdict string
	text    string
}

func readDocBlocks(t testing.TB) []docBlock {
	t.Helper()
	dir := filepath.Join("..", "..", "doc")
	paths, err := languageDocs(dir)
	if err != nil || len(paths) == 0 {
		t.Fatalf("no documents: %v", err)
	}
	var out []docBlock
	for _, p := range paths {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		for i := 0; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "```mk" {
				continue
			}
			start := i + 1
			var b strings.Builder
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				b.WriteString(lines[i])
				b.WriteByte('\n')
			}
			text := b.String()
			first, _, _ := strings.Cut(text, "\n")
			v, _ := strings.CutPrefix(strings.TrimSpace(first), "// verdict:")
			out = append(out, docBlock{name: p + ":" + itoa(start), verdict: strings.TrimSpace(v), text: text})
		}
	}
	return out
}

// testPreludeBlock is the reference's test prelude block, found by its
// Take and Res declarations.
func testPreludeBlock(t testing.TB, blocks []docBlock) string {
	for _, b := range blocks {
		if strings.HasPrefix(b.name, "reference.md:") && strings.Contains(b.text, "Take[T](x: T)") && strings.Contains(b.text, "linear struct Res") {
			return b.text
		}
	}
	t.Fatal("no test prelude block in doc/reference.md")
	return ""
}

var markerRE = regexp.MustCompile(`// <- ((?:E\d{4}[ ,]*)+)`)

var genLineRE = regexp.MustCompile(`(?m)^gen `)

// markers returns the "line:code" pairs a block's markers name.
func markers(text string, offset int) []string {
	var out []string
	for i, line := range strings.Split(text, "\n") {
		m := markerRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, c := range regexp.MustCompile(`E\d{4}`).FindAllString(m[1], -1) {
			out = append(out, itoa(i+1+offset)+":"+c)
		}
	}
	sort.Strings(out)
	return out
}

// namesCodes are the codes resolve owns in the names phase plus E0208.
func resolveOwns(c diag.Code) bool {
	switch c {
	case diag.E0201, diag.E0204, diag.E0206, diag.E0207, diag.E0208, diag.E1209, diag.E1221, diag.E0101, diag.E0000:
		return true
	}
	return false
}

// knownConflicts are doc blocks whose verdict contradicts a language rule,
// each to be fixed in doc/. The extra codes are asserted exactly, so
// the test fails when either the resolver or the block changes.
var knownConflicts = map[string][]string{
	// None.
}

// notJudged are doc blocks the resolver alone cannot judge, each with why;
// mk corpus judges them. An entry whose block passes here, or that names no
// block, fails the test, so the list stays current. A key is "doc.md: line",
// one of the block's lines trimmed, so an edit that moves the block keeps
// the key (docExcepted resolves it).
var notJudged = map[string]string{
	"reference.md: return xs.unwrap() // <- E1221":         "a member synonym's E1221 is check's, after the member lookup failed",
	"reference.md: Pop = 1 // <- E0206":                    "the duplicate discriminant's E0206 is check's, after the values are known",
	"reference.md: import std.fs // <- E0208":              "this world has no std.fs, and the resolver reports no unused import of a module it could not load",
	"reference.md: requires Now(io) > 0 // <- E0101":       "the E0101 of a function call in a requires is check's (a contract's form)",
	"reference.md: export Describe(g: mut Gen, t: Type) {": "a generator function: Gen and Type are std's generator API, which this harness's prelude does not declare",
	"reference.md: export Count(g: mut Gen, t: Type) {":    "a generator function: Gen and Type are std's generator API, which this harness's prelude does not declare",
}

// docExcepted keys list's entries by block name ("doc.md:<fence line>"). An
// entry "doc.md: line" names the one block of doc.md that has a line equal
// to line, trimmed; an entry that names no block, or several, fails the
// test.
func docExcepted(t testing.TB, blocks []docBlock, list map[string]string) map[string]string {
	out := map[string]string{}
	for k, why := range list {
		doc, line, ok := strings.Cut(k, ".md: ")
		if !ok {
			t.Errorf("notJudged key %q is not \"doc.md: line\"", k)
			continue
		}
		var found []string
		for _, blk := range blocks {
			if !strings.HasPrefix(blk.name, doc+".md:") {
				continue
			}
			for _, l := range strings.Split(blk.text, "\n") {
				if strings.TrimSpace(l) == line {
					found = append(found, blk.name)
					break
				}
			}
		}
		if len(found) != 1 {
			sort.Strings(found)
			t.Errorf("notJudged entry %q names %d blocks (%s), not one", k, len(found), strings.Join(found, ", "))
			continue
		}
		out[found[0]] = why
	}
	return out
}

func TestCorpusDocBlocks(t *testing.T) {
	t.Parallel()
	blocks := readDocBlocks(t)
	prelude := testPreludeBlock(t, blocks)
	excepts := docExcepted(t, blocks, notJudged)
	n := 0
	for _, blk := range blocks {
		if blk.verdict != "ok" && !strings.HasPrefix(blk.verdict, "reject") {
			continue
		}
		// A block with a gen line names what the generator writes, which
		// the resolver alone never sees; the driver's block tests run it.
		if genLineRE.MatchString(blk.text) {
			continue
		}
		n++
		blk := blk
		t.Run(blk.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := w.add("main", opt{testPrelude: prelude, parseErrors: true}, blk.text)
			var got []string
			for _, d := range b.parsed {
				// A parse-phase E0101 is a doc block's own verdict (some of
				// the reference's syntax blocks); internal/syntax's corpus
				// test pins every other parse code.
				if d.Code == diag.E0101 {
					got = append(got, itoa(int(b.srcs[1].LineCol(d.At.Start).Line))+":"+d.Code.String())
				}
			}
			for _, d := range b.diags {
				// The driver stops a module at its parse errors, so the
				// names pass over a recovered tree is not judged.
				if len(b.parsed) > 0 && d.Code != diag.E0000 {
					continue
				}
				if d.At.File != b.srcs[1].ID && d.Code != diag.E0000 {
					t.Errorf("diagnostic outside the block: %v %s", d.Code, d.Message)
					continue
				}
				if d.Phase == diag.PhaseNames || d.Code == diag.E0208 || d.Code == diag.E0000 {
					got = append(got, itoa(int(b.srcs[1].LineCol(d.At.Start).Line))+":"+d.Code.String())
				}
			}
			sort.Strings(got)
			want := append([]string(nil), knownConflicts[blk.name]...)
			for _, m := range markers(blk.text, 0) {
				_, code, _ := strings.Cut(m, ":")
				if c, ok := diag.ParseCode(code); ok && resolveOwns(c) {
					want = append(want, m)
				}
			}
			sort.Strings(want)
			why, excepted := excepts[blk.name]
			switch same := strings.Join(got, " ") == strings.Join(want, " "); {
			case same && excepted:
				t.Errorf("%s passes now: remove it from notJudged (%s)", blk.name, why)
			case excepted:
				t.Logf("%s: not judged here, %s: got %v, want %v", blk.name, why, got, want)
			case !same:
				t.Errorf("%s (%s): got %v, want %v\n%s%s", blk.name, blk.verdict, got, want, b.dump(), blk.text)
			}
		})
	}
	if n < 90 {
		t.Errorf("only %d ok and reject blocks", n)
	}
}

// TestCorpusPrograms resolves tests/programs/*.mk as bin roots: no
// names-phase diagnostic and no E0208 (their std imports are poisoned, as
// the driver poisons a module it could not load).
func TestCorpusPrograms(t *testing.T) {
	t.Parallel()
	paths, _ := filepath.Glob(filepath.Join("..", "..", "tests", "programs", "*.mk"))
	if len(paths) == 0 {
		t.Skip("no programs")
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		w := newWorld(t, true)
		b := w.add("main", opt{bin: true, parseErrors: true}, string(raw))
		for _, d := range b.diags {
			if d.Phase == diag.PhaseNames || d.Code == diag.E0208 || d.Code == diag.E0000 {
				t.Errorf("%s: %v@%s %s", filepath.Base(p), d.Code, b.lc(d.At), d.Message)
			}
		}
	}
}

// docFiles lists the Markdown files under dir, doc/std's pages included, as
// paths relative to dir with '/', in byte order: "reference.md",
// "std/fs.md".
func docFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	sort.Strings(out)
	return out, err
}

// languageDocs is docFiles without the std module pages (doc/std/): their
// examples call the real std, which this package's stand-in std does not
// declare, so mk corpus judges them instead.
func languageDocs(dir string) ([]string, error) {
	all, err := docFiles(dir)
	var out []string
	for _, f := range all {
		if !strings.HasPrefix(f, "std/") {
			out = append(out, f)
		}
	}
	return out, err
}
