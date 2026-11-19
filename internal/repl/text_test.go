// Why: the REPL must split typed text into entries and map scratch-file
// locations back to what the user typed.
// What: tests of the entry reader, Classify, import names, the location
// rewrite and the fuel clause, all in text.go.
// How: table cases over text.go's functions, with no session.

package repl

import (
	"bufio"
	"strings"
	"testing"
)

// TestReaderEntries: an entry continues while a bracket or a """
// string is open, or a line ends in an operator or a comma; brackets inside
// strings, chars and comments do not count; blank lines between entries are
// skipped; an entry left open at the end of input is still returned.
func TestReaderEntries(t *testing.T) {
	in := "1 + 2\n\nF(x: int): int {\n    return x // }\n}\n\"(\" + \"[\"\n'{'\n1 +\n    2\nG(a,\n  b)\n\"\"\"\nx {\n\"\"\"\nr\"a\\\" + \")\"\n(open\n"
	r := newReader(bufio.NewScanner(strings.NewReader(in)))
	var got []string
	conts := 0
	for {
		e, ok := r.next(func(cont bool) {
			if cont {
				conts++
			}
		})
		if !ok {
			break
		}
		got = append(got, e)
	}
	want := []string{
		"1 + 2",
		"F(x: int): int {\n    return x // }\n}",
		`"(" + "["`,
		"'{'",
		"1 +\n    2",
		"G(a,\n  b)",
		"\"\"\"\nx {\n\"\"\"",
		`r"a\" + ")"`,
		"(open",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries:\n%q\nwant:\n%q", got, want)
	}
	if conts == 0 {
		t.Error("no continuation prompt asked for")
	}
}

// TestClassify: item keywords and doc lines are declarations; a
// function (no keyword) is not classified here (the session tries it as an
// expression first); declName finds what a later entry replaces.
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		entry string
		decl  bool
		name  string
	}{
		{"struct P {\n    x: int\n}", true, "P"},
		{"export linear struct Q {\n}", true, "Q"},
		{"const N = 3", true, "N"},
		{"error Bad", true, "Bad"},
		{"/// doc\nF(): int { return 1 }", true, "F"},
		{"impl Eq for P {\n}", true, ""},
		{"test \"t\" {\n}", true, ""},
		{"F(x: int): int { return x }", false, "F"},
		{"export G[T](x: T): T { return x }", true, "G"},
		{"P.Len(&self): int { return 0 }", false, "P.Len"},
		{"1 + 2", false, ""},
		{"Sq(3)", false, "Sq"},
	} {
		if got := looksLikeDecl(c.entry); got != c.decl {
			t.Errorf("looksLikeDecl(%q) = %v", c.entry, got)
		}
		if got := declName(c.entry); got != c.name {
			t.Errorf("declName(%q) = %q, want %q", c.entry, got, c.name)
		}
	}
}

// TestImportNames: the name an import binds, and the use test that
// decides whether the import is written into the scratch file.
func TestImportNames(t *testing.T) {
	for _, c := range []struct{ entry, name string }{
		{"import std.coll", "coll"},
		{"import c = std.coll", "c"},
		{"import src.util", "util"},
		{"  import std.math  ", "math"},
	} {
		name, _, ok := parseImport(c.entry)
		if !ok || name != c.name {
			t.Errorf("parseImport(%q) = %q %v", c.entry, name, ok)
		}
	}
	for _, bad := range []string{"import", "import std.coll extra", "import 1x"} {
		if _, _, ok := parseImport(bad); ok {
			t.Errorf("parseImport(%q) accepted", bad)
		}
	}
	for _, c := range []struct {
		text string
		use  bool
	}{
		{"coll.Deque", true},
		{"x := coll.New()", true},
		{"mycoll.X", false},
		{"a.coll.X", false},
		{"coll", false},
		{"(coll.X)", true},
	} {
		if got := uses(c.text, "coll"); got != c.use {
			t.Errorf("uses(%q) = %v", c.text, got)
		}
	}
}

// TestLocationRewrite: scratch-file locations become <repl N>:line:col
// whatever directory precedes the file name; a line the REPL wrote for the
// entry loses its line and column; other files are untouched.
func TestLocationRewrite(t *testing.T) {
	orig := []origin{{}, {3, 1}, {3, 2}, {4, 0}, {4, 1}}
	lr := newLocRewriter(".mk-repl-9.mk", orig)
	in := ".mk-repl-9.mk:2:5: E0201 x\nC:/tmp/d/.mk-repl-9.mk:3:1 and .mk-repl-9.mk:5\nat (.mk-repl-9.mk:4:7) main.mk:2:9"
	want := "<repl 3>:1:5: E0201 x\n<repl 3>:2:1 and <repl 4>:1\nat (<repl 4>) main.mk:2:9"
	if got := lr.rewrite(in); got != want {
		t.Errorf("rewrite:\n%s\nwant:\n%s", got, want)
	}
}

// TestClauseBeforeComment: the fuel clause is written before a
// trailing comment, never inside it or inside a string; looksLikeFunc
// tells a function head from a call.
func TestClauseBeforeComment(t *testing.T) {
	for _, c := range []struct{ line, want string }{
		{"F(1)", "F(1) fuel 9"},
		{"F(1) // c", "F(1) fuel 9 // c"},
		{`F("a // b")`, `F("a // b") fuel 9`},
		{`F('/') // c`, `F('/') fuel 9 // c`},
	} {
		if got := withClause(c.line, "fuel 9"); got != c.want {
			t.Errorf("withClause(%q) = %q, want %q", c.line, got, c.want)
		}
	}
	for _, c := range []struct {
		entry string
		fn    bool
	}{
		{"G(): int {\n    return 1 2\n}", true},
		{"P.Len(&self): int {", true},
		{"G()", false},
		{"G(1) + 2", false},
	} {
		if got := looksLikeFunc(c.entry); got != c.fn {
			t.Errorf("looksLikeFunc(%q) = %v", c.entry, got)
		}
	}
}
