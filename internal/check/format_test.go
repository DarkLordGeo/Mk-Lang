// Why: a format string is checked at compile time against its record, so
// the format argument must be a constant check can read.
// What: TestE0706_FormatString, TestE0705_FormatArgument,
// TestForwardedFormat.
// How: checks Print and Arena.Fmt calls and compares codes; a forwarded
// const str parameter is accepted because mono judges the text per instance.

package check

import (
	"testing"

	"mk/internal/diag"
)

// TestE0706_FormatString: the format string's scan and match, one E0706 per
// call at the format argument, the first problem in the spec's order.
func TestE0706_FormatString(t *testing.T) {
	pre := "F(var out: Writer, i: int, s: str, x: f64): WriteError!void {\n"
	cases := []bodyCase{
		{"fizz", pre + "\ttry Print(mut out, \"fizzbuzz\\n\", ())\n\ttry Print(mut out, \"{i}\\n\", (i))\n}\n", ""},
		{"wc", pre + "\ttry Print(mut out, \"{word} {count}\\n\", (word: s, count: i))\n}\n", ""},
		{"braces and specs", pre + "\ttry Print(mut out, \"{{{i:x}}} {x:.2} {i}\", (i, x))\n}\n", ""},
		{"hole names no field", pre + "\ttry Print(mut out, \"{x} {y}\\n\", (x: 1))\n}\n", "E0706@2:21"},
		{"field used by no hole", pre + "\ttry Print(mut out, \"{x}\\n\", (x: 1, z: 2))\n}\n", "E0706@2:21"},
		{"lone open", pre + "\ttry Print(mut out, \"{i\", (i))\n}\n", "E0706@2:21"},
		{"lone close", pre + "\ttry Print(mut out, \"i}\", (i))\n}\n", "E0706@2:21"},
		{"bad name", pre + "\ttry Print(mut out, \"{1}\", (i))\n}\n", "E0706@2:21"},
		{"unknown spec", pre + "\ttry Print(mut out, \"{i:q}\", (i))\n}\n", "E0706@2:21"},
		{"x on a float", pre + "\ttry Print(mut out, \"{x:x}\", (x))\n}\n", "E0706@2:21"},
		{".N on an int", pre + "\ttry Print(mut out, \"{i:.2}\", (i))\n}\n", "E0706@2:21"},
		{"named const", "const Fmt = \"{i}\"\n" + pre + "\ttry Print(mut out, Fmt, (i))\n}\n", ""},
		{"named const mismatch", "const Fmt: str = \"{j}\"\n" + pre + "\ttry Print(mut out, Fmt, (i))\n}\n", "E0706@3:21"},
		{"generic argument", "G[A: Fields[Format] + Plain](var out: Writer, a: A): WriteError!void {\n\ttry Print(mut out, \"{i}\", a)\n}\n", "E0706@2:21"},
		{"arena fmt", "F(a: &Arena, i: int): OutOfMemory!str {\n\treturn try a.Fmt(\"{i} {j}\", (i))\n}\n", "E0706@2:19"},
	}
	runBodyCases(t, cases)
	w := newWorld(t, true)
	b := checkBody(t, w, "E0706@2:21", pre+"\ttry Print(mut out, \"{x} {y}\\n\", (x: 1))\n}\n")
	d := b.bodyDiags(t)[0]
	if fact(d, "problem") != "hole names no field: y" || fact(d, "holes") != "[x y]" || fact(d, "fields") != "[x]" {
		t.Fatalf("facts:\n%s", dump(w, []*diag.Diagnostic{d}))
	}
}

// TestE0705_FormatArgument: the format argument must be a string literal
// or a named str const, or a forwarded const str parameter; a
// local, a plain parameter or a copy of the const parameter is E0705.
func TestE0705_FormatArgument(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"local", "F(var out: Writer, fmt: str): WriteError!void {\n\ttry Print(mut out, fmt, (x: 1))\n}\n", "E0705@2:21"},
		{"forwarded const", "F(var out: Writer, const f: str): WriteError!void {\n\ttry Print(mut out, f, (x: 1))\n}\n", ""},
		{"bound local", "F(var out: Writer, const f: str): WriteError!void {\n\tg := f\n\ttry Print(mut out, g, (x: 1))\n}\n", "E0705@3:21"},
	})
}

// TestForwardedFormat: a `const` str parameter passed on as Print's or
// Arena.Fmt's format is accepted in the body whatever the record, generic
// or with no holes known, because mono judges the text
// per instance (internal/mono TestForwardedFormat*); a literal format with a generic
// record stays E0706, and a const parameter of another type is no format.
func TestForwardedFormat(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"generic record", "G[A: Fields[Format] + Plain](var out: Writer, const f: str, a: A): WriteError!void {\n\ttry Print(mut out, f, a)\n}\n", ""},
		{"short form", "F(var out: Writer, const f: str): WriteError!void {\n\ttry Print(mut out, f)\n}\n", ""},
		{"arena fmt", "F(a: &Arena, const f: str, i: int): OutOfMemory!str from a {\n\treturn try a.Fmt(f, (i))\n}\n", ""},
		{"method", "struct L {\n\tn: int\n}\n\nL.Info[A: Fields[Format] + Plain](&self, var out: Writer, const f: str, a: A): WriteError!void {\n\ttry Print(mut out, f, a)\n}\n", ""},
		{"literal, generic record", "G[A: Fields[Format] + Plain](var out: Writer, a: A): WriteError!void {\n\ttry Print(mut out, \"{i}\", a)\n}\n", "E0706@2:21"},
		{"const int", "F(var out: Writer, const f: int): WriteError!void {\n\ttry Print(mut out, f, (x: 1))\n}\n", "E0301@2:21 E0705@2:21"},
	})
}
