// Why: cli.Args prints a field's /// comment as its help, so generators need
// to read field doc comments, and an edit of a comment must rerun them.
// What: tests of Field.Doc: which comment counts as a field's doc
// (TestStdDynDocAbove) and a generator in Mk reading it end to end
// (TestStdDynFieldDocEndToEnd).
// How: the end-to-end test runs over std/core/core_gen.mk, appending
// Field.Doc's declaration if it is missing.

package gen

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// TestStdDynDocAbove: the lexer's doc-comment rule on the lines above a
// declaration: exactly three slashes (a fourth makes a plain comment), any
// indentation, "///" and one space removed, a plain comment, a blank line
// or code ending the run, and nothing for a declaration that does not start
// its line.
func TestStdDynDocAbove(t *testing.T) {
	for _, c := range []struct{ name, text, decl, want string }{
		{"none", "struct S {\n    x: int\n}\n", "x: int", ""},
		{"one", "struct S {\n    /// The x.\n    x: int\n}\n", "x: int", "The x."},
		{"two", "struct S {\n    /// First.\n    ///   Indented.\n    x: int\n}\n", "x: int", "First.\n  Indented."},
		{"empty-line", "struct S {\n    ///\n    /// After.\n    x: int\n}\n", "x: int", "\nAfter."},
		{"no-space", "struct S {\n    ///tight\n    x: int\n}\n", "x: int", "tight"},
		{"four-slashes", "struct S {\n    //// banner\n    x: int\n}\n", "x: int", ""},
		{"four-ends-run", "struct S {\n    /// Kept? No.\n    //// banner\n    x: int\n}\n", "x: int", ""},
		{"plain-between", "struct S {\n    /// Lost.\n    // plain\n    x: int\n}\n", "x: int", ""},
		{"blank-between", "struct S {\n    /// Lost.\n\n    x: int\n}\n", "x: int", ""},
		{"previous-field", "struct S {\n    /// A.\n    a: int\n    b: int\n}\n", "b: int", ""},
		{"crlf", "struct S {\r\n    /// Windows.\r\n    x: int\r\n}\r\n", "x: int", "Windows."},
		{"first-line", "/// Top.\nx: int\n", "x: int", "Top."},
		{"mid-line", "struct S { /// no\n    a: int, x: int\n}\n", "x: int", ""},
		{"tab", "struct S {\n\t/// Tabbed.\n\tx: int\n}\n", "x: int", "Tabbed."},
		// An exported field's span starts at its name, after
		// `export `, so the keyword before it still starts the line.
		{"export", "struct S {\n    /// Shown.\n    export x: int\n}\n", "x: int", "Shown."},
		{"export-tab", "struct S {\n\t/// Shown.\n\texport\tx: int\n}\n", "x: int", "Shown."},
		{"export-two-blanks", "struct S {\n    /// Shown.\n    export  x: int\n}\n", "x: int", "Shown."},
		{"export-glued", "struct S {\n    /// No.\n    exportx: int\n}\n", "x: int", ""},
		{"export-after-field", "struct S {\n    /// No.\n    a: int, export x: int\n}\n", "x: int", ""},
		{"other-word", "struct S {\n    /// No.\n    pub x: int\n}\n", "x: int", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			off := strings.Index(c.text, c.decl)
			if off < 0 {
				t.Fatalf("no %q in the text", c.decl)
			}
			if got := docAbove(c.text, uint32(off)); got != c.want {
				t.Errorf("docAbove = %q, want %q", got, c.want)
			}
		})
	}
	if got := docAbove("x", 5); got != "" {
		t.Errorf("an offset past the text: %q", got)
	}
}

// docGen emits, for struct T, `T.Docs(): []str` returning every field's
// Field.Doc in declaration order, so the test reads them back from the
// generated text.
const docGen = `export Docs(g: mut Gen, t: Type) {
    var body := g.Block()
    for f in t.Fields() {
        body.Add(quote {
            n += $(g.Str(f.Doc())).Len()
        })
    }
    g.Emit(quote {
        $(t.Name()).DocLen(): int {
            var n := 0
            $body
            return n
        }
    })
}
`

const docUser = `import doc

struct Opts {
    /// Print more.
    /// Twice for debug.
    verbose: bool
    //// not a doc
    quiet: bool
    /// Lost: a plain comment follows.
    // plain
    port: int

    /// The name "quoted".
    name: str
}

gen doc.Docs(Opts)
`

// TestStdDynFieldDocEndToEnd: a generator in Mk reads each field's doc
// comment through Field.Doc on eval, the text reaches the output as a str
// literal (escapes included), and the run records a ReadFile of the
// struct's file with its content hash, so an edit of a comment alone
// invalidates the output (comments change no token hash, so nothing else
// would).
func TestStdDynFieldDocEndToEnd(t *testing.T) {
	core := readRepo(t, "std/core/core_gen.mk")
	if !strings.Contains(core, "Field.Doc(") {
		core += "\nexport Field.Doc(&self): str\n"
	}
	w := newMkWorldCore(t, core)
	w.add("doc", false, map[string]string{"doc.mk": docGen})
	m := w.addLoose("main", false, map[string]string{"main.mk": docUser})
	text := mustGen(t, w, m)
	for _, want := range []string{
		`n += "Print more.\nTwice for debug.".Len()`,
		`n += "".Len()`,
		`n += "The name \"quoted\".".Len()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %s:\n%s", want, text)
		}
	}
	if got := strings.Count(text, `n += "".Len()`); got != 2 {
		t.Errorf("want 2 fields without a doc (quiet, port), got %d:\n%s", got, text)
	}
	var file []types.Read
	for _, r := range m.gen.Lines[0].Reads.Sorted() {
		if r.Kind == types.ReadFile {
			file = append(file, r)
		}
	}
	if len(file) != 1 || file[0].Name != "main/main.mk" && !strings.HasSuffix(file[0].Name, "main.mk") || file[0].Hash.IsZero() {
		t.Errorf("want one ReadFile of main.mk with its hash, got %+v", file)
	}
}
