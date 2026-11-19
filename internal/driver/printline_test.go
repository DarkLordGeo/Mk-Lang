// Why: an unpadded Print should cost direct calls, not a runner over a piece
// table; the release IR shows whether it does.
// What: TestPrintStraightLine_ReleaseIR and fnBody, which cuts one define out
// of the IR.
// How: builds a small program in release, reads each function's IR for
// Writer.Str and the value calls, and compares its output in dev and release.

package driver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/types"
)

const printLineMain = `import std.text

Unpadded(w: mut Writer, a: int, h: u64, s: str): WriteError!void {
    try Print(w, "a={a} h={h:x} s={s}\n", (a, h, s))
}

Padded(w: mut Writer, a: int): WriteError!void {
    try Print(w, "[{a:>6}]\n", (a))
}

Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

export Main(sys: &System): WriteError!void {
    var store: [256]u8 = [0; 256]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    try Unpadded(mut w, -42, 0xBEEF, "ok")
    try Padded(mut w, 7)
    try Say(mut w, "say {a}|", (a: 5))
    try sys.Print("sys {b}\n", (b: 6))
    var out := sys.Stdout()
    try out.Bytes(c.Bytes())
}
`

// fnBody is the text of the define of sym in ll, through its closing
// brace; "" when ll does not define it.
func fnBody(ll, sym string) string {
	re := regexp.MustCompile(`(?m)^define [^\n]*@"?` + regexp.QuoteMeta(sym) + `"?\(`)
	loc := re.FindStringIndex(ll)
	if loc == nil {
		return ""
	}
	rest := ll[loc[0]:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end+3]
	}
	return rest
}

// TestPrintStraightLine_ReleaseIR: in the release pack IR, a function whose
// only Print has no width calls Writer.Str and the value functions directly
// and names neither the runner nor a piece table; a padded Print in the same
// program still calls FmtPadRun; a forwarded const text (a user forwarder
// and System.Print, expanded per instance by mono) is straight-line in its
// instance too; and the program prints the same bytes in dev and release.
func TestPrintStraightLine_ReleaseIR(t *testing.T) {
	tools := needTools(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(printLineMain), 0o644); err != nil {
		t.Fatal(err)
	}
	llDir := filepath.Join(t.TempDir(), "ll")
	cfg := Config{Root: file, Profile: types.Release, Tools: tools, CacheDir: t.TempDir()}
	cfg.TestHooks.LLDir = llDir
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "main.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("release build:\n%s", r.Report.Text(r.Sources))
	}
	_, files := readLLDir(t, llDir)
	var plain, padded string
	for _, b := range files {
		if p := fnBody(string(b), "mk.main.Unpadded"); p != "" {
			plain = p
		}
		if p := fnBody(string(b), "mk.main.Padded"); p != "" {
			padded = p
		}
	}
	if plain == "" || padded == "" {
		t.Fatalf("no define of mk.main.Unpadded or mk.main.Padded in the pack IR")
	}
	for _, bad := range []string{"FmtRun", "FmtPadRun", "FmtPiece", "FmtArg"} {
		if strings.Contains(plain, bad) {
			t.Errorf("Unpadded names %s:\n%s", bad, plain)
		}
	}
	// Four literals, three holes: a=, h=, s= and the newline through
	// Writer.Str; a and s through FmtValue, h through FmtSpecValue.
	if n := strings.Count(plain, `@"mk.std.core.Writer:Str"(`); n != 4 {
		t.Errorf("Unpadded calls Writer.Str %d times, want 4:\n%s", n, plain)
	}
	if n := strings.Count(plain, "@mk.std.core.FmtValue."); n != 2 {
		t.Errorf("Unpadded calls FmtValue %d times, want 2:\n%s", n, plain)
	}
	if n := strings.Count(plain, "@mk.std.core.FmtSpecValue."); n != 1 {
		t.Errorf("Unpadded calls FmtSpecValue %d times, want 1:\n%s", n, plain)
	}
	if !strings.Contains(padded, "@mk.std.core.FmtPadRun(") {
		t.Errorf("Padded does not call FmtPadRun:\n%s", padded)
	}
	// The forwarded texts: Say's one instance ("say ", a, "|") and the
	// System.Print instance of "sys {b}\n" ("sys ", b, "\n"), each with
	// two Writer.Str calls and one FmtValue call, and no runner.
	for _, prefix := range []string{"mk.main.Say.", "mk.std.core.System:Print."} {
		var bodies []string
		for _, b := range files {
			re := regexp.MustCompile(`(?m)^define [^\n]*@"?` + regexp.QuoteMeta(prefix) + `[0-9a-f]+"?\(`)
			for _, loc := range re.FindAllStringIndex(string(b), -1) {
				rest := string(b)[loc[0]:]
				if end := strings.Index(rest, "\n}\n"); end >= 0 {
					rest = rest[:end+3]
				}
				bodies = append(bodies, rest)
			}
		}
		if len(bodies) != 1 {
			t.Errorf("%d instances %s*, want 1", len(bodies), prefix)
			continue
		}
		body := bodies[0]
		if strings.Contains(body, "FmtRun") || strings.Contains(body, "FmtPiece") ||
			strings.Count(body, `@"mk.std.core.Writer:Str"(`) != 2 || strings.Count(body, "@mk.std.core.FmtValue.") != 1 {
			t.Errorf("%s instance is not straight-line (2 Writer.Str, 1 FmtValue, no FmtRun):\n%s", prefix, body)
		}
	}
	want := "sys 6\na=-42 h=beef s=ok\n[     7]\nsay 5|"
	if out, _, code := runExe(t, types.X86_64Windows, paths[0], ""); out != want || code != 0 {
		t.Errorf("release: exit %d, stdout %q, want %q", code, out, want)
	}
	exe := buildProgram(t, file, types.X86_64Windows, types.Dev, t.TempDir())
	if out, _, code := runExe(t, types.X86_64Windows, exe, ""); out != want || code != 0 {
		t.Errorf("dev: exit %d, stdout %q, want %q", code, out, want)
	}
}
