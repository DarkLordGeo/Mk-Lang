// Why: gen regex.Matcher compiles a pattern to Mk code at build time, which
// must behave as the run-time matcher and Go's regexp do.
// What: the generated IsMatch and Find against Go's regexp, and the
// generator's failures.
// How: generated and hand cases; the output names regex.Match, since a
// literal name in a quote may name the generator's own module.

package std

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rxGenBuild builds src against the test's std copy with a cache of its own
// (a shared cache has given stale-entry ICEs while std is being edited).
// It returns the executable, or the
// output and exit status when the build fails and wantFail is set.
func rxGenBuild(t *testing.T, src string, wantFail bool, flags ...string) (string, string) {
	t.Helper()
	root := t.TempDir()
	stdDir := regexStdCopy(t, root)
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main.exe")
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if wantFail {
		if status == 0 {
			t.Fatalf("mk build succeeded, want a failure")
		}
		return "", out
	}
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe, ""
}

// rxGenFuel is the fuel of each gen line: the default 4,000,000 steps
// cover a small pattern such as the bench row's, not every generated one
// (a Unicode class splits the alphabet into many byte classes).
const rxGenFuel = 200_000_000

// rxGenLine is the model's line for matcher i on input j: IsMatch, then
// Find's offsets or -.
func rxGenLine(re *regexp.Regexp, i, j int, s string) string {
	line := fmt.Sprintf("M%d I%d %v", i, j, re.MatchString(s))
	if loc := re.FindStringIndex(s); loc != nil {
		return line + fmt.Sprintf(" %d,%d", loc[0], loc[1])
	}
	return line + " -"
}

// rxGenUnicodePats are the Matcher test's patterns over Unicode tables.
var rxGenUnicodePats = []string{`\p{Greek}+`, `(?i)σ+`, `\p{Nd}{2}`, `(?i)straſe`, `[\p{Han}a]+`, `(?i)ǆ\b`}

// TestSTDREGEX_MatcherDifferential: 60 generated patterns and the hand
// patterns that fit the DFA budget, one gen regex.Matcher line each, their
// IsMatch and Find on every input against Go, in a release build.
func TestSTDREGEX_MatcherDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	g := &rxGen{r: r}
	var pats []string
	for _, p := range rxEdgePats {
		if p == `(a|b)*a(a|b){12}` || p == `(a|b){12}a(a|b)*` || p == `[a-q][^u-z]{13}x` {
			continue // over the budget: TestSTDREGEX_MatcherFails
		}
		pats = append(pats, p)
	}
	// Unicode classes and folds that fit the generator's budget; a whole
	// category such as \pL does not (512 states per DFA, or the fuel), so
	// the generated patterns leave \p and \P out.
	pats = append(pats, rxGenUnicodePats...)
	base := len(pats)
	for len(pats) < base+60 {
		p := g.pattern()
		if strings.Contains(p, `\p`) || strings.Contains(p, `\P`) {
			continue
		}
		if _, err := regexp.Compile(p); err == nil {
			pats = append(pats, p)
		}
	}
	ins := append([]string{}, rxEdgeIns...)
	for len(ins) < 30 {
		ins = append(ins, rxInput(r))
	}
	var src, calls strings.Builder
	src.WriteString("import std.regex\n\n")
	var want []string
	for i, p := range pats {
		fmt.Fprintf(&src, "struct M%d {}\n\ngen regex.Matcher(M%d, %s) fuel %d\n\n", i, i, rxQuote(p), rxGenFuel)
		fmt.Fprintf(&calls, "        try Line(mut out, %d, j, M%d.IsMatch(s), M%d.Find(s))\n", i, i, i)
		re := regexp.MustCompile(p)
		for j, s := range ins {
			want = append(want, rxGenLine(re, i, j, s))
		}
	}
	// want is pattern-major; the program prints input-major, so sort it so.
	var ordered []string
	for j := range ins {
		for i := range pats {
			ordered = append(ordered, want[i*len(ins)+j])
		}
	}
	fmt.Fprintf(&src, `const Ins: []str = %s

Line(out: mut Writer, i: int, j: int, m: bool, f: ?regex.Match): WriteError!void {
    try Print(out, "M{i} I{j} {m}", (i, j, m))
    if x := f {
        lo := x.lo
        hi := x.hi
        try Print(out, " {lo},{hi}\n", (lo, hi))
    } else {
        try Print(out, " -\n")
    }
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for j in 0..Ins.Len() {
        s := Ins[j]
%s    }
}
`, rxStrs(ins), calls.String())
	exe, _ := rxGenBuild(t, src.String(), false, "-release")
	got := regexExec(t, exe)
	t.Logf("%d matchers, %d inputs", len(pats), len(ins))
	regexCompare(t, got, strings.Join(ordered, "\n")+"\n", func(line string) string {
		var i, j int
		if _, err := fmt.Sscanf(line, "M%d I%d", &i, &j); err != nil || i >= len(pats) || j >= len(ins) {
			return "?"
		}
		return fmt.Sprintf("pattern %q, input %q", pats[i], ins[j])
	})
}

// TestSTDREGEX_MatcherFails: a pattern with a syntax error and one whose
// reverse DFA is over the budget fail the gen line with their messages.
func TestSTDREGEX_MatcherFails(t *testing.T) {
	for _, c := range []struct{ pat, want string }{
		{`a(b`, "syntax error at byte 1 of the pattern"},
		{`(a|b){12}a(a|b)*`, "over the generator's budget"},
	} {
		src := fmt.Sprintf("import std.regex\n\nstruct M {}\n\ngen regex.Matcher(M, %s) fuel %d\n\nexport Main(sys: &System) {\n    _ = M.IsMatch(\"a\")\n}\n", rxQuote(c.pat), rxGenFuel)
		_, out := rxGenBuild(t, src, true)
		if !strings.Contains(out, c.want) || !strings.Contains(out, "E0903") {
			t.Errorf("%s: want E0903 with %q, got\n%s", c.pat, c.want, out)
		}
	}
}
