// Why: Print-shaped functions (a `const f: str` then a record bounded by
// Fields[Format]) must accept a struct with no Format when the format text
// prints it by field paths.
// What: TestPathsThroughAStructWithNoFormat, TestPrintShapedBody,
// TestFormatBoundCases.
// How: the Fields[Format] bound is judged by the call's format text, hole by
// hole; a Print-shaped body passes its record on only with its format,
// unchanged, to another Print-shaped call (E0705 at the definition
// otherwise).

package check

import (
	"strings"
	"testing"
)

const formatBoundTypes = `struct Row {
	export name: str
	export qty: int
	export price: f64
}

struct Bare {
	export n: int
}

struct Wrap {
	export b: Bare
}

struct Sys {
	n: int
}

Sys.Print[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, args: A): WriteError!void {
	try Print(w, f, args)
}

struct Logger {
	n: int
}

Logger.Info[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, args: A): WriteError!void {
	try Line(w, f, args)
}

Line[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
	try w.Str("> ")
	try Print(w, f, args)
}

Outer[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
	try Mid(w, f, args)
}

Mid[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
	try Print(w, f, args)
}

`

// formatBoundLine is the line of the call in formatBoundCall's body.
var formatBoundLine = strings.Count(formatBoundTypes, "\n") + 2

// formatBoundCall is a body that makes one call through each callee form.
func formatBoundCall(call string) string {
	return formatBoundTypes + "F(var out: Writer, a: &Arena, s: &Sys, l: &Logger, r: Row, w: Wrap): WriteError|OutOfMemory!void {\n\t" + call + "\n}\n"
}

func TestPathsThroughAStructWithNoFormat(t *testing.T) {
	type form struct{ name, pre, mid, post string }
	forms := []form{
		{"Print", "try Print(mut out, ", ", ", ")"},
		{"sys.Print", "try s.Print(mut out, ", ", ", ")"},
		{"Logger.Info", "try l.Info(mut out, ", ", ", ")"},
		{"a two-level forwarder", "try Outer(mut out, ", ", ", ")"},
		{"Arena.Fmt", "_ = try a.Fmt(", ", ", ")"},
	}
	var cases []bodyCase
	for _, fm := range forms {
		col := len(fm.pre) + 2 // the format argument, after the tab
		at := "E0706@" + itoa(formatBoundLine) + ":" + itoa(col)
		mk := func(text, rec string) string {
			return formatBoundCall(fm.pre + `"` + text + `"` + fm.mid + rec + fm.post)
		}
		cases = append(cases,
			bodyCase{fm.name + ": paths through a struct with no Format", mk(`{r.name:<12}{r.qty:>6}{r.price:>10.2}\n`, "(r)"), ""},
			bodyCase{fm.name + ": a bare hole on it", mk(`{r}`, "(r)"), at},
			bodyCase{fm.name + ": a path ending at no Format", mk(`{w.b}`, "(w)"), at},
			bodyCase{fm.name + ": a path through it to a Format field", mk(`{w.b.n}`, "(w)"), ""},
		)
	}
	runBodyCases(t, cases)
	w := newWorld(t, true)
	b := checkBody(t, w, "E0706@"+itoa(formatBoundLine)+":21", formatBoundCall(`try Print(mut out, "{r} {r.name}", (r))`))
	if d := b.bodyDiags(t)[0]; fact(d, "problem") != "field without Format: r" {
		t.Fatalf("facts:\n%s", dump(w, b.bodyDiags(t)))
	}
}

// TestPrintShapedBody: what a Print-shaped body may do with its
// record. Passing f and args on together is the one use that needs
// Fields[Format]; anything else is E0705 at the definition, so a call's
// acceptance never depends on a body.
func TestPrintShapedBody(t *testing.T) {
	head := "Bad[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {\n"
	dump := "Dump[A: Fields[Format] + Plain](w: mut Writer, args: A): WriteError!void {\n\tfor name, value in fields(&args) {\n\t\ttry w.Str(name)\n\t\ttry value.Format(w)\n\t}\n}\n\n"
	other := "Other(const s: str): int {\n\treturn 0\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"passes both on", head + "\ttry Print(w, f, args)\n\ttry Line(w, f, args)\n}\n\n" + formatBoundTypes, ""},
		{"passes a copy of the record on", head + "\tb := args\n\ttry Print(w, f, b)\n}\n\n" + formatBoundTypes, ""},
		{"uses f as a value", head + "\ttry w.Str(f)\n\ttry Print(w, f, args)\n}\n\n" + formatBoundTypes, ""},
		{"a fields loop over the record", head + "\tfor name, value in fields(&args) {\n\t\ttry w.Str(name)\n\t}\n}\n", "E0705@2:2"},
		{"the record to a Fields[Format] generic", dump + head + "\ttry Dump(w, args)\n}\n", "E0705@9:6"},
		{"f with another record", head + "\ttry Print(w, f, (x: 1))\n}\n", "E0705@2:18"},
		{"f with no record", head + "\ttry Print(w, f)\n}\n", "E0705@2:6"},
		{"the record with a literal", head + "\ttry Print(w, \"{x}\", args)\n}\n", "E0705@2:6"},
		{"the record with another const str", "Bad[A: Fields[Format] + Plain](w: mut Writer, const g: str, const f: str, args: A): WriteError!void {\n\ttry Print(w, g, args)\n}\n", "E0705@2:18"},
		{"f to another const parameter", other + head + "\t_ = Other(f)\n}\n", "E0705@6:12"},
		{"a fields loop in a literal", head + "\tg := fn(x: A) {\n\t\tfor name, value in fields(&x) {\n\t\t}\n\t}\n}\n", "E0705@3:3"},
		{"the record's type to a type bounded by Fields[Format]", "struct Box[T: Fields[Format] + Plain] {\n\tv: T\n}\n\n" + head + "\tb := Box[A](v: args)\n\t_ = b\n}\n", "E0705@6:7"},
		{"the record to a method of a Fields[Format] type", "struct Box[T: Fields[Format] + Plain] {\n\tv: T\n}\n\nBox[T].Show(&self, w: mut Writer): WriteError!void {\n\tfor name, value in fields(&self.v) {\n\t\ttry w.Str(name)\n\t\ttry value.Format(w)\n\t}\n}\n\n" + head + "\tb: Box[A] = Box(v: args)\n\ttry b.Show(w)\n}\n", "E0705@13:14 E0705@13:5 E0705@14:6"},
		{"the record to a Fields[Format] generic in a contract", "Count[A: Fields[Format] + Plain](args: A): int {\n\tvar n := 0\n\tfor name, value in fields(&args) {\n\t\tn += name.Len()\n\t\t_ = value\n\t}\n\treturn n\n}\n\nBad[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void requires Count(args) >= 0 {\n\ttry Print(w, f, args)\n}\n", "E0705@10:96"},
		{"a generic not of the Print shape keeps its loop", dump + "F(var out: Writer): WriteError!void {\n\ttry Dump(mut out, (n: 1))\n}\n", ""},
		{"and its bound, as written", dump + formatBoundTypes + "F(var out: Writer, r: Row): WriteError!void {\n\ttry Dump(mut out, (r))\n}\n", "E0701@" + itoa(8+formatBoundLine-1) + ":6"},
		{"the record passed where only Plain is needed", "Keep[T: Plain](t: T): T {\n\treturn t\n}\n\n" + head + "\tx := Keep(args)\n\ttry Print(w, f, x)\n}\n", ""},
		{"a method of a generic owner with its own record, a fields loop", "struct Box[T] {\n\texport x: T\n}\n\nBox[T].P[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, args: A): WriteError!void {\n\tfor name, value in fields(&args) {\n\t\ttry w.Str(name)\n\t\ttry value.Format(w)\n\t}\n\ttry Print(w, f, args)\n}\n", "E0705@6:2"},
	})
}

// TestFormatBoundCases: A format that is no constant keeps its E0705 when
// the record also fails its bound; an explicit type argument for the record
// is judged by the text as well; a method whose record parameter is its
// generic owner's takes the bound as written (E0701 where the owner's type
// is formed with a struct of no Format), and its text is judged per
// instance; a forwarder of another shape needs the bound as written.
func TestFormatBoundCases(t *testing.T) {
	owner := "struct L[A: Fields[Format] + Plain] {\n\texport n: int\n}\n\nL[A].P(&self, w: mut Writer, const f: str, args: A): WriteError!void {\n\ttry Print(w, f, args)\n}\n\n"
	at := itoa(formatBoundLine+8) + ":"
	runBodyCases(t, []bodyCase{
		{"a local as the format, a bad record", formatBoundCall("x := \"a\"\n\ttry s.Print(mut out, x, 5)"), "E0701@" + itoa(formatBoundLine+1) + ":6 E0705@" + itoa(formatBoundLine+1) + ":23"},
		{"a local as Print's format, a bad record", formatBoundCall("x := \"a\"\n\ttry Print(mut out, x, 5)"), "E0701@" + itoa(formatBoundLine+1) + ":6 E0705@" + itoa(formatBoundLine+1) + ":21"},
		{"an explicit record type", formatBoundCall("try s.Print[(r: Row)](mut out, \"{r.name}\", (r))"), ""},
		{"an owner's record parameter, bound as written", owner + formatBoundCall("o := L[(r: Row)](n: 1)\n\ttry o.P(mut out, \"{r.name}\", (r))"), "E0701@" + at + "7 E0701@" + itoa(formatBoundLine+9) + ":6"},
		{"an owner's record parameter with Format", owner + formatBoundCall("o := L[(n: int)](n: 1)\n\ttry o.P(mut out, \"{n}\", (n: 2))"), ""},
		{"a forwarder of another shape", "G(w: mut Writer, const f: str, r: Row): WriteError!void {\n\ttry Print(w, f, (r))\n}\n\n" + formatBoundTypes, "E0701@2:6"},
	})
}
