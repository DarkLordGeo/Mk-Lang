// Why: the body queries walk a Print's calls, and an unpadded Print lowers to
// direct calls, not one runner call; the answers must follow them.
// What: TestQuery_StraightLinePrint.
// How: queries a small program's bodies and checks the calls each Print reaches,
// direct for an unpadded one and FmtPadRun for a padded one.

package driver

import (
	"strings"
	"testing"
)

// TestQuery_StraightLinePrint pins what the body queries say about a Print
// whose text has no width, which is lowered straight-line
// (internal/lower/format.go): they follow its direct calls, Writer.Str and
// the value functions, instead of reaching FmtRun and stopping at its calls
// through FmtArg (counted as indirect). A padded Print reaches FmtPadRun.
// The walk is over generic bodies (a generic function's sites are listed
// from its generic body), so FmtValue's `if T is f64` arm, which the
// FmtValue[int] instance prunes, is walked too: a may-reach answer, as for
// any generic callee.
func TestQuery_StraightLinePrint(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"qpkg"`, "bins": `[pkg.Bin(name: "qpkg", root: "src/main", link_script: "", cpu: "", subsystem: "console")]`}),
		"src/main/main.mk": `struct Tag {
    export id: int
}

impl Format for Tag {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("<")
        try self.id.Format(w)
        try w.Str(">")
    }
}

Hello(w: mut Writer, n: int, t: Tag): WriteError!void {
    try Print(w, "n={n} t={t}\n", (n, t))
}

Lit(w: mut Writer): WriteError!void {
    try Print(w, "plain\n", ())
}

Pad(w: mut Writer, n: int): WriteError!void {
    try Print(w, "[{n:>4}]", (n))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try Hello(mut out, 1, Tag(id: 2))
    try Lit(mut out)
    try Pad(mut out, 3)
}
`,
	})
	s, err := NewSession(Config{Root: dir, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(q, arg string) string {
		t.Helper()
		a, res, err := s.Query(q, []string{arg})
		if err != nil || !a.Found {
			t.Fatalf("%s %s: %v %+v\n%s", q, arg, err, a, diagLines(res))
		}
		return strings.Join(a.Answer, "\n")
	}
	for _, c := range []struct {
		q, arg    string
		want, not []string
	}{
		{"stack", "Hello", []string{"Hello -> FmtValue"}, []string{"FmtRun"}},
		{"stack", "Lit", []string{"Lit -> Writer.Str -> Writer.Bytes"}, []string{"FmtRun", "FmtValue"}},
		{"stack", "Pad", []string{"FmtPadRun"}, nil},
		{"panics", "Hello", []string{"in Hello -> FmtValue"}, []string{"FmtRun"}},
		{"panics", "Lit", []string{"in Lit -> Writer.Str"}, []string{"FmtRun"}},
		{"callers", "Writer.Str", []string{"Hello  src/main/main.mk:14", "Lit  src/main/main.mk:18"}, []string{"Pad  "}},
	} {
		got := ask(c.q, c.arg)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s lacks %q:\n%s", c.q, c.arg, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s %s names %q:\n%s", c.q, c.arg, n, got)
			}
		}
	}
}
