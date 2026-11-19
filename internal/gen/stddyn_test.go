// Why: std/dyn's generator found bugs in gen: a stale keyword table, missing
// docs of imported fields and a file hashed once per field.
// What: regression tests that the keyword table of std/dyn/dyn.mk matches
// the lexer, that Field.Doc works on an imported struct's exported fields,
// and that Field.Doc hashes its file once per run, with
// BenchmarkFieldDocWide.
// How: modules built by mkWorld over docCore; the benchmark uses a
// 1,000-field struct in a file of about 1 MB.

package gen

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/source"
	"mk/internal/types"
)

// docCore is core_gen.mk with Field.Doc declared (appended if the file
// lacks it).
func docCore(t testing.TB) string {
	core := readRepo(t, "std/core/core_gen.mk")
	if !strings.Contains(core, "Field.Doc(") {
		core += "\nexport Field.Doc(&self): str\n"
	}
	return core
}

// TestImportedStructFieldDoc: Field.Doc of a struct declared in another
// module, whose fields a generator sees only when exported, gives each
// field's doc. The declaration view's field span starts at the name, after
// `export `, which docAbove once took for a declaration that does not start
// its line, so every field of an imported struct had "".
func TestImportedStructFieldDoc(t *testing.T) {
	w := newMkWorldCore(t, docCore(t))
	// docFree is docGen emitting a free function under a fresh name, since a
	// module cannot declare a method of another module's type.
	w.add("doc", false, map[string]string{"doc.mk": `export Docs(g: mut Gen, t: Type) {
    var body := g.Block()
    for f in t.Fields() {
        body.Add(quote {
            n += $(g.Str(f.Doc())).Len()
        })
    }
    name := g.Fresh("DocLen")
    g.Emit(quote {
        $name(): int {
            var n := 0
            $body
            return n
        }
    })
}
`})
	w.add("opts", false, map[string]string{"opts.mk": `export struct Opts {
    /// Print more.
    export verbose: bool
    /// The port.
    /// Two lines.
    export port: int
    export quiet: bool
}
`})
	m := w.addLoose("main", false, map[string]string{"main.mk": `import doc
import opts

struct Local {
    /// Local doc.
    export loud: bool
}

gen doc.Docs(opts.Opts)
gen doc.Docs(Local)
`})
	text := mustGen(t, w, m)
	for _, want := range []string{
		`n += "Print more.".Len()`,
		`n += "The port.\nTwo lines.".Len()`,
		`n += "Local doc.".Len()`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %s:\n%s", want, text)
		}
	}
	if got := strings.Count(text, `n += "".Len()`); got != 1 {
		t.Errorf("want 1 field without a doc (quiet), got %d:\n%s", got, text)
	}
}

// TestDocFileHashOncePerFile: Field.Doc hashes the declaring file once
// per run, whatever the number of fields, and gives every call the same
// hash, the file's ReadFile hash; another file is hashed on its own.
func TestDocFileHashOncePerFile(t *testing.T) {
	r := &run{}
	text := strings.Repeat("x", 1<<16)
	a := r.docFileHash(1, text)
	for range 1000 {
		if h := r.docFileHash(1, text); h != a {
			t.Fatalf("hash changed between calls: %s, %s", a, h)
		}
	}
	if r.docHashes != 1 {
		t.Errorf("1001 calls on one file took %d hashes, want 1", r.docHashes)
	}
	if want := source.HashBytes(fileReadDomain, []byte(text)); a != want {
		t.Errorf("memoized hash %s, want the file's ReadFile hash %s", a, want)
	}
	if b := r.docFileHash(2, "y"); b == a || r.docHashes != 2 {
		t.Errorf("a second file: hash %s (first %s), %d hashes, want a new hash and 2", b, a, r.docHashes)
	}
}

// wideDocSrc is a module whose struct Wide has n fields, each with a doc
// line, after a padding comment of pad bytes, and a gen line over it.
func wideDocSrc(n, pad int) string {
	var b strings.Builder
	b.WriteString("import doc\n\n// " + strings.Repeat("p", pad) + "\n\nstruct Wide {\n")
	for i := range n {
		fmt.Fprintf(&b, "    /// Field %d.\n    fld%d: int\n", i, i)
	}
	b.WriteString("}\n\ngen doc.Docs(Wide)\n")
	return b.String()
}

// TestWideStructOneRead: a gen line calling Field.Doc on each of 200
// fields records one ReadFile read, and every field's doc reaches the output.
func TestWideStructOneRead(t *testing.T) {
	w := newMkWorldCore(t, docCore(t))
	w.add("doc", false, map[string]string{"doc.mk": docGen})
	m := w.addLoose("main", false, map[string]string{"main.mk": wideDocSrc(200, 1<<12)})
	text := mustGen(t, w, m)
	for _, i := range []int{0, 99, 199} {
		if want := fmt.Sprintf(`n += "Field %d.".Len()`, i); !strings.Contains(text, want) {
			t.Errorf("output lacks %s", want)
		}
	}
	files := 0
	for _, r := range m.gen.Lines[0].Reads.Sorted() {
		if r.Kind == types.ReadFile {
			files++
		}
	}
	if files != 1 {
		t.Errorf("want one ReadFile read, got %d", files)
	}
}

// BenchmarkFieldDocWide is one gen line calling Field.Doc on every
// field of a 1,000-field struct in a file of about 1 MB (the struct and a
// padding comment): the whole module, from parse to the checked output.
// Field.Doc hashes the file once per run, not per call.
func BenchmarkFieldDocWide(b *testing.B) {
	core := docCore(b)
	src := wideDocSrc(1000, 1<<20)
	for b.Loop() {
		w := newMkWorldCore(b, core)
		w.add("doc", false, map[string]string{"doc.mk": docGen})
		m := w.addLoose("main", false, map[string]string{"main.mk": src})
		if len(m.diags) > 0 || m.gen == nil {
			b.Fatalf("diagnostics: %s", w.dump(m.diags))
		}
	}
}

// TestKeywordTableMatchesLexer: std/dyn/dyn.mk's Reserved holds the
// lexer's every keyword (ast.Keyword: FirstKeyword..LastKeyword and asm) and
// the contextual words, so a table slot named after a method (Error is
// error, Is is is) never reaches Gen.Ident as a keyword. A keyword added to
// the lexer and not to the table fails here, not as E0901 in a user's gen
// line.
func TestKeywordTableMatchesLexer(t *testing.T) {
	src := readRepo(t, "std/dyn/dyn.mk")
	start := strings.Index(src, "\nReserved(")
	if start < 0 {
		t.Fatal("std/dyn/dyn.mk has no Reserved function")
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatal("Reserved has no end")
	}
	var table []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(src[start:start+end], -1) {
		table = append(table, m[1])
	}
	var want []string
	for k := ast.FirstKeyword; k <= ast.LastKeyword; k++ {
		want = append(want, k.FixedText())
	}
	want = append(want, ast.TokAsm.FixedText(), ast.TokBecome.FixedText()) // become
	// The contextual words (ast.Keyword's comment): not keywords, but the
	// table escapes them so a slot never reads as one where they mean
	// something (self.table.self, a record field named from).
	want = append(want, "from", "into", "ends", "fuel", "packed", "self", "result", "fields")
	for _, w := range want {
		if !slices.Contains(table, w) {
			t.Errorf("Reserved lacks %q", w)
		}
	}
	for _, w := range table {
		if _, kw := ast.Keyword(w); !kw && !slices.Contains(want, w) {
			t.Errorf("Reserved holds %q, neither a keyword nor a contextual word", w)
		}
	}
}
