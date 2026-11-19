// Why: Print and Arena.Fmt have a short form without the argument record,
// which must cost nothing over the () form.
// What: TestShortFormSameMIR, TestForwardedFormatStaysACall.
// How: lowers both forms and compares their MIR text; a forwarded const
// format stays a plain call.

package lower

import (
	"strings"
	"testing"
)

// TestShortFormSameMIR: Print(w, "x") and Print(w, "x", ()) lower to
// equal MIR, and so do a.Fmt("x") and a.Fmt("x", ()), with Validate on.
func TestShortFormSameMIR(t *testing.T) {
	src := `ShortPrint(w: mut Writer): WriteError!void {
	try Print(w, "done\n")
}
LongPrint(w: mut Writer): WriteError!void {
	try Print(w, "done\n", ())
}
ShortFmt(a: &Arena): OutOfMemory!str from a {
	return try a.Fmt("x {{}}")
}
LongFmt(a: &Arena): OutOfMemory!str from a {
	return try a.Fmt("x {{}}", ())
}
ShortNs(a: &Arena): OutOfMemory!str from a {
	return try Arena.Fmt(a, "x")
}
LongNs(a: &Arena): OutOfMemory!str from a {
	return try Arena.Fmt(a, "x", ())
}
`
	l := lowerFmt(t, src)
	for _, pair := range [][2]string{{"ShortPrint", "LongPrint"}, {"ShortFmt", "LongFmt"}, {"ShortNs", "LongNs"}} {
		short, long := l.print(pair[0]), l.print(pair[1])
		// The printed names differ only in the item's own name.
		long = strings.ReplaceAll(long, pair[1], pair[0])
		if short != long {
			t.Errorf("%s and %s differ:\n%s\n----\n%s", pair[0], pair[1], short, long)
		}
		// Print is straight-line (format.go), Arena.Fmt keeps its runner.
		if !strings.Contains(short, "Writer:Str") && !strings.Contains(short, "FmtArena") {
			t.Errorf("%s: no Writer.Str or runner call:\n%s", pair[0], short)
		}
	}
}

// TestForwardedFormatStaysACall: a `const` str parameter forwarded as
// Print's or Arena.Fmt's format has no text at lower time, so the
// call stays a plain call of Print or Arena.Fmt with the parameter as its
// format operand, which mono expands per instance (internal/mono
// TestForwardedFormatExpands); a literal format is still synthesized.
func TestForwardedFormatStaysACall(t *testing.T) {
	src := `Fwd(w: mut Writer, const f: str): WriteError!void {
	try Print(w, f, (x: 1))
}
FwdGeneric[A: Fields[Format] + Plain](w: mut Writer, const f: str, a: A): WriteError!void {
	try Print(w, f, a)
}
FwdShort(w: mut Writer, const f: str): WriteError!void {
	try Print(w, f)
}
FwdFmt(a: &Arena, const f: str): OutOfMemory!str from a {
	return try a.Fmt(f, (n: 2))
}
Lit(w: mut Writer): WriteError!void {
	try Print(w, "{x}", (x: 1))
}
`
	l := lowerFmt(t, src)
	for _, name := range []string{"Fwd", "FwdGeneric", "FwdShort", "FwdFmt"} {
		p := l.print(name)
		if strings.Contains(p, "FmtRun") || strings.Contains(p, "FmtArena") || strings.Contains(p, "FmtValue") || strings.Contains(p, "Writer:Str") {
			t.Errorf("%s: a forwarded format was synthesized:\n%s", name, p)
		}
		if !strings.Contains(p, "std.core.Print") && !strings.Contains(p, "Arena:Fmt") {
			t.Errorf("%s: no plain formatter call:\n%s", name, p)
		}
	}
	if p := l.print("Lit"); !strings.Contains(p, "call std.core.FmtValue[int](") {
		t.Errorf("Lit: no straight-line call:\n%s", p)
	}
}
