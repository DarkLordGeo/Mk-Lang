// Why: these are the REPL's edge cases: where an entry ends, what is
// rewritten, and when stale state must be refused.
// What: TestRepl* tests of main stubs, generic methods, value
// rewrites, signature continuation, stale scratch files, termination, const
// re-evaluation and comment-only entries.
// How: scripted sessions and direct calls of the reader.

package repl

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReplMainStubOnlyForOwnModule: a package whose second bin root has no
// Main reports that module's E0201, and the session does not add its empty
// Main to the root module, which declares its own (no E0206 of its making).
func TestReplMainStubOnlyForOwnModule(t *testing.T) {
	manifest := strings.Replace(testManifest, `bins: [pkg.Bin(name: "pk", root: ".", link_script: "", cpu: "", subsystem: "console")]`,
		`bins: [pkg.Bin(name: "pk", root: ".", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "tool", root: "cmd/tool", link_script: "", cpu: "", subsystem: "console")]`, 1)
	dir := writeTree(t, map[string]string{
		"pkg.mk":        manifest,
		"main.mk":       "export Main(sys: &System) {\n}\n",
		"cmd/tool/t.mk": "Tool(): int {\n    return 1\n}\n",
	})
	out, errOut, code := pipeSession(t, Options{Root: dir}, "1 + 2\n")
	if code != 0 {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "3")
	if strings.Contains(errOut, "E0206") || strings.Contains(errOut, "<repl") {
		t.Errorf("the session made a diagnostic of its own:\n%s", errOut)
	}
	if !strings.Contains(errOut, "cmd/tool/t.mk:1:1: E0201") || strings.Count(errOut, ": E0") != 1 {
		t.Errorf("want only cmd/tool/t.mk's E0201 outside the session:\n%s", errOut)
	}
}

// TestReplGenericTypeMethod: a method of a generic type, Holder[T].Size,
// is named Holder.Size, so it joins the struct instead of replacing it, and a
// second method of the same type does not replace the first.
func TestReplGenericTypeMethod(t *testing.T) {
	script := "struct Holder[T] {\n    v: T\n}\nHolder[T].Size(&self): int { return 1 }\nHolder[T].Two(&self): int { return 2 }\nHolder[int](v: 5).Size()\nHolder[int](v: 5).Two()\n"
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 || strings.Contains(errOut, "<repl") {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "1", "2")
	for _, c := range []struct{ entry, name string }{
		{"Holder[T].Size(&self): int { return 1 }", "Holder.Size"},
		{"Pair[K, V].Swap[U](&self, u: U): int {", "Pair.Swap"},
		{"Box2[T].New(v: T): Box2[T] {", "Box2.New"},
	} {
		if got := declName(c.entry); got != c.name {
			t.Errorf("declName(%q) = %q, want %q", c.entry, got, c.name)
		}
	}
}

// TestReplValuesNotRewritten: a str value that spells the standalone
// scratch file's name and a line is printed as it is, and so is the sig
// answer of a const holding one.
func TestReplValuesNotRewritten(t *testing.T) {
	script := "\"repl.mk:1\"\n\"see myrepl.mk:2:3\"\n\"x repl.mk:5:3 y\"\nconst S = \"repl.mk:7\"\n:query sig S\n"
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 || strings.Contains(errOut, "<repl") {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	g := lines(out)
	if len(g) < 4 || strings.Join(g[:3], "\n") != "\"repl.mk:1\"\n\"see myrepl.mk:2:3\"\n\"x repl.mk:5:3 y\"" || !strings.Contains(g[3], `"repl.mk:7"`) {
		t.Errorf("stdout:\n%s", out)
	}
}

// TestReplRewriteBoundary: the rewriter takes the file's name only at a
// word boundary and outside a "string".
func TestReplRewriteBoundary(t *testing.T) {
	lr := newLocRewriter("repl.mk", []origin{{}, {2, 1}})
	in := "repl.mk:2:5: E0201 x\nmyrepl.mk:2:3 my-repl.mk:2 a.repl.mk:2\n  | \"repl.mk:2\" + repl.mk:2\n"
	want := "<repl 2>:1:5: E0201 x\nmyrepl.mk:2:3 my-repl.mk:2 a.repl.mk:2\n  | \"repl.mk:2\" + <repl 2>:1\n"
	if got := lr.rewrite(in); got != want {
		t.Errorf("rewrite:\n%s\nwant:\n%s", got, want)
	}
}

// TestReplSignatureContinuation: a function whose contracts sit on the
// lines under its signature, with its body's { on a line of its own
// is one entry and is kept.
func TestReplSignatureContinuation(t *testing.T) {
	script := "Div(a: int, b: int): int\n    requires b != 0\n{\n    return a / b\n}\nDiv(6, 3)\nHalf(a: int): int\n    requires a >= 0\n    ensures result <= a\n{\n    return a / 2\n}\nHalf(9)\n"
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 || strings.Contains(errOut, "<repl") {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "2", "4")
}

// TestReplReaderSignature: the reader keeps a signature's continuation
// lines and its own-line { in one entry; a call is never read past (it
// would wait for input before its answer); a signature followed by a line
// that does not continue it ends there, and that line starts the next entry.
func TestReplReaderSignature(t *testing.T) {
	in := "Div(a: int, b: int): int\n    requires b != 0\n{\n    return a / b\n}\n" +
		"Log(x: int)\n    requires x > 0\n{\n}\n" +
		"Pick(a: &Arena): str\n    from a\n{\n    return \"\"\n}\n" +
		"R(x: int): int\n    requires (x > 0 &&\n        x < 10)\n{\n    return x\n}\n" +
		"Sq(12)\n" +
		"F(x: int): int\n1 + 1\n" +
		"G(): int\n\n2\n"
	r := newReader(bufio.NewScanner(strings.NewReader(in)))
	var got []string
	for {
		e, ok := r.next(func(bool) {})
		if !ok {
			break
		}
		got = append(got, e)
	}
	want := []string{
		"Div(a: int, b: int): int\n    requires b != 0\n{\n    return a / b\n}",
		"Log(x: int)\n    requires x > 0\n{\n}",
		"Pick(a: &Arena): str\n    from a\n{\n    return \"\"\n}",
		"R(x: int): int\n    requires (x > 0 &&\n        x < 10)\n{\n    return x\n}",
		"Sq(12)",
		"F(x: int): int",
		"1 + 1",
		"G(): int",
		"2",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries:\n%q\nwant:\n%q", got, want)
	}
	for _, c := range []struct {
		entry string
		sig   bool
	}{
		{"Sq(12)", false},
		{"P(x: 1, y: 2).y", false},
		{"Holder[int](v: 5).Size()", false},
		{"Div(a: int, b: int): int", true},
		{"G[T](x: T): T { return x }", true},
		{"F(a: &A) from a {", true},
		{"P(x, y) := p", false},
	} {
		if got := isSignature(c.entry, false); got != c.sig {
			t.Errorf("isSignature(%q) = %v", c.entry, got)
		}
	}
}

// TestReplStaleScratchRefused: another session's scratch file left in the
// package (its process killed with no chance to clean up) is named and the
// session refuses to start on top of it (status 2), leaving it in place; a
// file of the user's that only shares the prefix is no scratch file.
func TestReplStaleScratchRefused(t *testing.T) {
	stale := scratchHeader + "\nconst MkRepl3 =\nnope + 1\n"
	dir := writeTree(t, map[string]string{
		"pkg.mk":            testManifest,
		"main.mk":           "export Main(sys: &System) {\n}\n",
		".mk-repl-99999.mk": stale,
		".mk-repl-notes.mk": "// the user's own file, not a scratch file\nconst UserNote = 1\n",
	})
	out, errOut, code := pipeSession(t, Options{Root: dir}, "1 + 2\n")
	if code != 2 || out != "" || !strings.Contains(errOut, ".mk-repl-99999.mk is another mk repl session's scratch file") {
		t.Fatalf("status %d, stdout %q, stderr:\n%s", code, out, errOut)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".mk-repl-99999.mk")); err != nil || string(got) != stale {
		t.Errorf("the stale file changed: %q %v", got, err)
	}
	os.Remove(filepath.Join(dir, ".mk-repl-99999.mk"))
	out, errOut, code = pipeSession(t, Options{Root: dir}, "1 + UserNote\n")
	if code != 0 {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	wantLines(t, out, "2")
}

// TestReplTerminateClosesSession: a termination or a hang-up (mk repl
// notifies SIGTERM and SIGHUP besides Ctrl+C; on Windows a console close
// arrives as SIGTERM) closes the session like Ctrl+C: the scratch file is
// gone and the status is 128 plus the signal's number.
func TestReplTerminateClosesSession(t *testing.T) {
	for _, c := range []struct {
		sig  os.Signal
		want int
	}{{syscall.SIGTERM, 143}, {syscall.SIGHUP, 129}, {os.Interrupt, 130}} {
		dir := writeTree(t, map[string]string{"pkg.mk": testManifest, "main.mk": "export Main(sys: &System) {\n}\n"})
		s, err := New(Options{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		var out, e bytes.Buffer
		stop := make(chan os.Signal, 1)
		code := make(chan int, 1)
		scratch := s.ScratchFile()
		go func() { code <- s.ServeUntil(inR, &out, &e, stop) }()
		stop <- c.sig
		select {
		case got := <-code:
			if got != c.want {
				t.Errorf("%v: status %d, want %d", c.sig, got, c.want)
			}
		case <-time.After(2 * time.Minute):
			t.Fatalf("%v: no return within 2 minutes", c.sig)
		}
		if _, err := os.Stat(scratch); err == nil {
			t.Errorf("%v: scratch file left behind", c.sig)
		}
		s.Close()
		inW.Close()
		inR.Close()
	}
}

// TestReplConstNotEvaluatedAgain: a kept const with a scalar or str value
// is written into the scratch file as that value, so later entries do not
// evaluate its initializer again. Proof by behaviour: after Count is
// redefined to loop forever, Big still reads 1000 (evaluating it again would
// be E0902), and :decls still shows the const as typed.
func TestReplConstNotEvaluatedAgain(t *testing.T) {
	s, err := New(standalone(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out, errOut bytes.Buffer
	s.Start(&bytes.Buffer{})
	for _, e := range []string{
		"Count(n: int): int {\n    var i := 0\n    for i < n {\n        i += 1\n    }\n    return i\n}",
		"const Big = Count(1000)",
		"const Name = \"a\\tb\"",
		"Count(n: int): int {\n    for {\n    }\n    return 0\n}",
		"Big + 1",
		"Name",
		":decls",
	} {
		s.Entry(e, &out, &errOut)
	}
	if errOut.Len() > 0 {
		t.Errorf("stderr:\n%s", errOut.String())
	}
	g := lines(out.String())
	if len(g) < 2 || g[0] != "1001" || g[1] != `"a\tb"` || !strings.Contains(out.String(), "const Big = Count(1000)\n") {
		t.Errorf("stdout:\n%s", out.String())
	}
	text, _, _ := s.compose(nil, nil)
	if !strings.Contains(text, "const Big: int = 1000\n") || strings.Contains(text, "Count(1000)") || !strings.Contains(text, "const Name: str = \"a\\tb\"\n") {
		t.Errorf("scratch text:\n%s", text)
	}
	for _, c := range []struct {
		typ, val string
		ok       bool
	}{
		{"int", "-7", true}, {"i8", "-128", false}, {"int", "-9223372036854775808", false}, {"u64", "18446744073709551615", true},
		{"bool", "true", true}, {"char", "'c'", true}, {"char", `'\x00'`, false}, {"str", `"\x01"`, false},
		{"str", `"é"`, true}, {"f64", "5", false}, {"P", "P{align 8 0x01}", false},
	} {
		if got := literalValue(c.typ, c.val); got != c.ok {
			t.Errorf("literalValue(%s, %s) = %v", c.typ, c.val, got)
		}
	}
}

// TestReplCommentOnlyEntry: an entry of comments alone is nothing: not
// kept, not listed by :decls, not numbered; a comment before an entry
// belongs to it.
func TestReplCommentOnlyEntry(t *testing.T) {
	out, errOut, code := pipeSession(t, standalone(t), "// note\n:decls\n1 + 1\n// before\nnope\n// last\n")
	if code != 0 {
		t.Fatalf("status %d", code)
	}
	wantLines(t, out, "2")
	if !strings.Contains(errOut, "<repl 2>:2:1: E0201") || strings.Count(errOut, "<repl ") != 1 {
		t.Errorf("stderr:\n%s", errOut)
	}
}
