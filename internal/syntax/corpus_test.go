// Why: every example in doc/ and every test program must parse, or the
// documentation shows code the compiler cannot read.
// What: TestLexCoversText and TestDocBlocksParse over every fenced
// mk block of doc/**/*.md and every program under tests/programs.
// How: lexing must cover the text and print it back; parsing must give no
// diagnostic beyond the parse-phase codes a reject block's verdict names.

package syntax

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/source"
)

// block is one fenced mk block of a design document.
type block struct {
	name    string // file:line
	verdict string // "ok", "parse", "reject E0501 ..."
	text    string
}

// repoRoot finds the module root from the package directory.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// docBlocks reads every fenced mk block of doc/**/*.md, in file and line
// order.
func docBlocks(t testing.TB) []block {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "doc")
	paths, err := docFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []block
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
			startLine := i + 1
			var b strings.Builder
			i++
			for ; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
				b.WriteString(lines[i])
				b.WriteByte('\n')
			}
			text := b.String()
			verdict := ""
			first, _, _ := strings.Cut(text, "\n")
			if v, ok := strings.CutPrefix(strings.TrimSpace(first), "// verdict:"); ok {
				verdict = strings.TrimSpace(v)
			}
			out = append(out, block{
				name:    p + ":" + itoa(startLine),
				verdict: verdict,
				text:    text,
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no mk blocks found under doc/")
	}
	return out
}

// programFiles reads tests/programs/*.mk.
func programFiles(t testing.TB) []block {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(repoRoot(t), "tests", "programs", "*.mk"))
	sort.Strings(paths)
	var out []block
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, block{name: filepath.Base(p), verdict: "ok", text: string(raw)})
	}
	return out
}

// TestLexCoversText checks token coverage and print-back on every corpus
// block. A lexical diagnostic is an error unless the block is a reject
// block whose verdict names its code and the diagnostic's line is marked
// `// <- CODE` (the reference's lexical E0101 blocks).
func TestLexCoversText(t *testing.T) {
	t.Parallel()
	for _, b := range append(docBlocks(t), programFiles(t)...) {
		text := source.Normalize([]byte(b.text))
		toks, diags := Lex(1, text)
		if err := ast.Validate(&ast.File{Base: ast.Base{NID: 1, Sp: source.Span{File: 1, End: uint32(len(text))}}, Tokens: toks, NodeCount: 1}, text); err != nil {
			t.Errorf("%s: %v", b.name, err)
		}
		codes, reject := strings.CutPrefix(b.verdict, "reject ")
		lines := strings.Split(text, "\n")
		for _, d := range diags {
			c := d.Code.String()
			ln := strings.Count(text[:min(int(d.At.Start), len(text))], "\n")
			marked := strings.Contains(lines[ln], "// <- "+c)
			if reject && slices.Contains(strings.Fields(codes), c) && marked {
				continue
			}
			t.Errorf("%s: lexical %v at %d: %s", b.name, d.Code, d.At.Start, d.Message)
		}
	}
}

// knownDesignConflicts are doc blocks whose verdict contradicts a language
// rule, each to be fixed in doc/. The test asserts the exact codes, so it
// fails when either the parser or the block changes.
var knownDesignConflicts = map[string]string{}

// TestDocBlocksParse parses every fenced mk block of doc/ (and
// every program of tests/programs) with zero diagnostics: verdict: parse
// blocks with AllowBodiless, ok and reject blocks without. A reject block
// may expect a parse-phase code (some of the reference's E0101 blocks):
// then every diagnostic the parser emits has a code the verdict names and
// sits on a line marked `// <- CODE`, and every line marked with one of the
// codes it emits gets that diagnostic.
func TestDocBlocksParse(t *testing.T) {
	t.Parallel()
	all := append(docBlocks(t), programFiles(t)...)
	for _, b := range all {
		t.Run(b.name, func(t *testing.T) {
			opts := Options{AllowBodiless: b.verdict == "parse"}
			r := parse(t, b.text, opts)
			want := knownDesignConflicts[b.name]
			if codes, ok := strings.CutPrefix(b.verdict, "reject "); ok && want == "" && len(r.diags) > 0 {
				named := strings.Fields(codes)
				lines := strings.Split(b.text, "\n")
				emitted := map[string]bool{} // code -> emitted at all
				at := map[string]bool{}      // "line:code" -> emitted there
				for _, d := range r.diags {
					c := d.Code.String()
					ln := int(r.src.LineCol(d.At.Start).Line)
					emitted[c] = true
					at[itoa(ln)+":"+c] = true
					if !slices.Contains(named, c) {
						t.Errorf("%s: %s at line %d is not in the verdict", b.name, c, ln)
					}
					if ln < 1 || ln > len(lines) || !strings.Contains(lines[ln-1], "// <- "+c) {
						t.Errorf("%s: %s not on a line marked for it", b.name, d.Code)
					}
				}
				for i, l := range lines {
					for c := range emitted {
						if strings.Contains(l, "// <- "+c) && !at[itoa(i+1)+":"+c] {
							t.Errorf("%s: line %d is marked %s, and the parser reports none there", b.name, i+1, c)
						}
					}
				}
				return
			}
			if got := r.codeList(); got != want {
				t.Errorf("%s (%s): got %q, want %q:\n  %s\n%s", b.name, b.verdict, got, want, strings.Join(r.codes(), "\n  "), b.text)
			}
		})
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
