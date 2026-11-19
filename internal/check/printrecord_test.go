// Why: Print and Arena.Fmt may leave out the record when the format string
// has no hole, and the short form must not change other calls.
// What: TestPrintNoRecord and TestImplicitRecordMarked.
// How: checks calls with and without holes and records and compares codes.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
)

// TestPrintNoRecord: the short form is the call with (); with
// a hole it is E0706 alone; the () form stays; a local
// format argument is E0705 alone; another function stays E0304.
func TestPrintNoRecord(t *testing.T) {
	pre := "F(var out: Writer, a: &Arena): WriteError|OutOfMemory!void {\n"
	runBodyCases(t, []bodyCase{
		{"print", pre + "\ttry Print(mut out, \"done\\n\")\n}\n", ""},
		{"fmt method", pre + "\ts := try a.Fmt(\"x\")\n\t_ = s\n}\n", ""},
		{"fmt namespace", pre + "\ts := try Arena.Fmt(a, \"x\")\n\t_ = s\n}\n", ""},
		{"named const", "const Done = \"done\\n\"\n\n" + pre + "\ttry Print(mut out, Done)\n}\n", ""},
		{"escaped braces", pre + "\ttry Print(mut out, \"{{}}\")\n}\n", ""},
		{"record form stays", pre + "\ttry Print(mut out, \"done\\n\", ())\n\ts := try a.Fmt(\"x\", ())\n\t_ = s\n}\n", ""},
		{"design Done", "Done(w: mut Writer, a: &Arena): WriteError|OutOfMemory!str from a {\n\ttry Print(w, \"done\\n\")\n\treturn try a.Fmt(\"no holes\")\n}\n", ""},
		{"hole", pre + "\tn := 3\n\ttry Print(mut out, \"{n} items\\n\")\n}\n", "E0706@3:21"},
		{"hole fmt", pre + "\tn := 3\n\ts := try a.Fmt(\"{n}\")\n\t_ = s\n}\n", "E0706@3:17"},
		{"local format", pre + "\tf := \"x\"\n\ttry Print(mut out, f)\n}\n", "E0705@3:21"},
		{"lone brace", pre + "\ttry Print(mut out, \"{\")\n}\n", "E0706@2:21"},
		{"other function", "G(w: mut Writer, f: str, x: int) {\n}\n\nF(var out: Writer) {\n\tG(mut out, \"x\")\n}\n", "E0304@5:2"},
		{"two short", pre + "\ttry Print(mut out)\n}\n", "E0304@2:6 E0711@2:6"}, // unchanged by the short form
	})
}

// TestImplicitRecordMarked: the short form marks its CallExpr for
// lower; the () form and a failed short form do not.
func TestImplicitRecordMarked(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "", "F(var out: Writer): WriteError!void {\n\ttry Print(mut out, \"a\")\n\ttry Print(mut out, \"b\", ())\n}\n")
	body := b.w.c.Check(b.m, b.item("F"))
	var marked []bool
	ast.Inspect(body.Node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			marked = append(marked, body.ImplicitRecord.Has(c.ID(), body.Base))
		}
		return true
	})
	if len(marked) != 2 || !marked[0] || marked[1] {
		t.Fatalf("ImplicitRecord per call %v, want [true false]", marked)
	}
	w2 := newWorld(t, true)
	b2 := checkBody(t, w2, "E0706@2:21", "F(var out: Writer, n: int): WriteError!void {\n\ttry Print(mut out, \"{n}\")\n}\n")
	d := b2.bodyDiags(t)[0]
	if fact(d, "problem") != "no record" || fact(d, "holes") != "[n]" || fact(d, "fields") != "[]" {
		t.Fatalf("facts:\n%s", dump(w2, []*diag.Diagnostic{d}))
	}
}
