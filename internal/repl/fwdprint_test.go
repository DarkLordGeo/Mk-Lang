// Why: the REPL evaluates entries, so a Print forwarded through a const
// parameter must run in the evaluator with the compiled program's bytes.
// What: TestForwardedPrintEntry.
// How: runs a script whose entries format through a forwarder into a Text and
// through an Arena.Fmt forwarder, and compares the bytes.

package repl

import (
	"strings"
	"testing"
)

// TestForwardedPrintEntry: an entry that formats through a forwarded Print
// (a function that passes its const format on) into a Text over
// EvalArena(), and one through an Arena.Fmt forwarder, give the bytes the
// compiled program would.
func TestForwardedPrintEntry(t *testing.T) {
	script := `Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}
Len(a: &Arena): int {
    var t := Text.New(a)
    var w := t.Writer()
    n := 42
    Say(mut w, "n={n}|{n:x}|{n:>4}", (n)) else {
        return -1
    }
    return t.Len()
}
AFmt[A: Fields[Format] + Plain](a: &Arena, const f: str, args: A): OutOfMemory!str from a {
    return try a.Fmt(f, args)
}
Len(EvalArena())
AFmt(EvalArena(), "x{n}y", (n: 5)).Must()
`
	out, errOut, code := pipeSession(t, standalone(t), script)
	if code != 0 || strings.Contains(errOut, "E0000") || strings.Contains(errOut, "<repl") {
		t.Fatalf("status %d, stderr:\n%s", code, errOut)
	}
	// "n=42|2a|  42" is 12 bytes.
	wantLines(t, out, "12", `"x5y"`)
}
