// Why: Print's short form (no record when the text has no hole) must hold
// for any callee of the Print shape, judged by signature, not by name.
// What: TestShortFormByShape, TestShortFormFixedRecord,
// TestFieldPaths, TestSpecGrammar.
// How: checks calls of Print-shaped and other callees and compares codes.

package check

import "testing"

// TestShortFormByShape: the short form holds for any callee whose
// last two parameters are a const str and a type parameter bounded by
// Fields[Format] (a function, a method, a forwarder of a forwarder); a text
// with a hole and no record is E0706 at the format argument; a callee of
// another shape (no Fields[Format] bound, a non-const format, Fields of
// another interface) keeps its arity, E0304.
func TestShortFormByShape(t *testing.T) {
	say := `Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, a: A): WriteError!void {
	try Print(w, f, a)
}

`
	runBodyCases(t, []bodyCase{
		{"function", say + `F(var out: Writer): WriteError!void {
	try Say(mut out, "done")
}
`, ""},
		{"function with record", say + `F(var out: Writer, i: int): WriteError!void {
	try Say(mut out, "{i}", (i))
}
`, ""},
		{"hole, no record", say + `F(var out: Writer): WriteError!void {
	try Say(mut out, "{i}")
}
`, "E0706@6:19"},
		{"forwarded twice", say + `T[A: Fields[Format] + Plain](w: mut Writer, const f: str, a: A): WriteError!void {
	try Say(w, f, a)
	try Say(w, f)
}
`, "E0705@7:6"},
		{"method", `struct L {
	n: int
}

L.Info[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, a: A): WriteError!void {
	try Print(w, f, a)
}

F(l: &L, var out: Writer): WriteError!void {
	try l.Info(mut out, "starting")
}
`, ""},
		{"not Fields[Format]", "P[A: Plain](const f: str, a: A) {\n}\n\nF() {\n\tP(\"x\")\n}\n", "E0304@5:2 E0711@5:2"},
		{"format not const", "P[A: Fields[Format] + Plain](f: str, a: A) {\n}\n\nF() {\n\tP(\"x\")\n}\n", "E0304@5:2 E0711@5:2"},
		{"Fields of Eq", "P[A: Fields[Eq] + Plain](const f: str, a: A) {\n}\n\nF() {\n\tP(\"x\")\n}\n", "E0304@5:2 E0711@5:2"},
	})
}

// TestShortFormFixedRecord: the short form leaves the record out only
// while its type is open. When an explicit type argument or the receiver
// fixes it to a record other than (), the call keeps its arity (E0304), and
// lower never passes () for a (x: int) parameter (these were
// E0000 in codegen and lower). Fixed to () it is still the short form.
func TestShortFormFixedRecord(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"function with an explicit record type", `F[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
	try w.Str(f)
}

G(var out: Writer): WriteError!void {
	try F[(x: int)](mut out, "hi\n")
}
`, "E0304@6:6"},
		{"method of an owner fixed by the receiver", `struct L[A: Fields[Format] + Plain] {
	n: int
}

L[A].M(&self, w: mut Writer, const f: str, args: A): WriteError!void {
	try w.Str(f)
}

G(var out: Writer): WriteError!void {
	l := L[(x: int)](n: 0)
	try l.M(mut out, "hi\n")
}
`, "E0304@11:6"},
		{"Print with an explicit record type", `G(var out: Writer): WriteError!void {
	try Print[(x: int)](mut out, "hi\n")
}
`, "E0304@2:6"},
		{"Print fixed to ()", `G(var out: Writer): WriteError!void {
	try Print[()](mut out, "hi\n")
}
`, ""},
		{"a forwarder fixed to its own parameter", `F[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
	try w.Str(f)
}

G[B: Fields[Format] + Plain](w: mut Writer, const f: str, b: B): WriteError!void {
	try F[B](w, "hi\n")
}
`, "E0304@6:6 E0705@6:6"},
	})
}

// TestFieldPaths: a hole names a field path through exported fields,
// any depth (through a shared reference: std's Print blocks, since this
// prelude has no Format for &T); every top-level field is used,
// counting paths; the problems in the format scan's order, E0706 at the format
// argument.
func TestFieldPaths(t *testing.T) {
	types := `struct Pt {
	export x: int
	export y: f64
	hid: int
}

struct Bare {
	export n: int
}

struct Row {
	export name: str
	export at: Pt
	export b: Bare
	export k: int
}

impl Format for Row {
	Format(&self, w: mut Writer): WriteError!void {
	}
}

impl Format for Pt {
	Format(&self, w: mut Writer): WriteError!void {
	}
}

`
	body := func(f, rec string) string {
		return types + "F(var out: Writer, r: Row, i: int): WriteError!void {\n\ttry Print(mut out, \"" + f + "\", " + rec + ")\n}\n"
	}
	at := "E0706@29:21"
	runBodyCases(t, []bodyCase{
		{"one level", body("{r.name} {r.k}", "(r)"), ""},
		{"two levels and a spec", body("{r.at.x:x} {r.at.y:.2} {r.name}", "(r)"), ""},
		{"the field and a path", body("{r} {r.name}", "(r)"), ""},
		{"unused sibling", body("{r.name}", "(r, i)"), at},
		{"unexported field", body("{r.at.hid}", "(r)"), at},
		{"unknown field", body("{r.nope}", "(r)"), at},
		{"through an int", body("{r.k.x}", "(r)"), at},
		{"through a str", body("{r.name.x}", "(r)"), at},
		{"leaf without Format", body("{r.b}", "(r)"), at},
		{"x on a path to a float", body("{r.at.y:x}", "(r)"), at},
		{".N on a path to an int", body("{r.at.x:.1}", "(r)"), at},
		{"an expression", body("{r.k + 1}", "(r)"), at},
		{"empty segment", body("{r..k}", "(r)"), at},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, at, body("{r.at.hid}", "(r)"))
	if d := b.bodyDiags(t)[0]; fact(d, "problem") != "path names no exported field: r.at.hid" || fact(d, "holes") != "[r.at.hid]" {
		t.Fatalf("facts:\n%s", dump(w, b.bodyDiags(t)))
	}
}

// TestSpecGrammar: [[fill]align][+][0][width][.N][type], each rule
// accepted and refused at the literal (E0706 at the format argument), on
// each kind of field: an integer, a float, a str and a user Format.
func TestSpecGrammar(t *testing.T) {
	types := `struct Pt {
	export x: int
}

impl Format for Pt {
	Format(&self, w: mut Writer): WriteError!void {
	}
}

`
	body := func(f string) string {
		return types + "F(var out: Writer, i: int, u: int, x: f64, s: str, p: Pt): WriteError!void {\n\ttry Print(mut out, \"" + f + " {i}{u}{x}{s}{p}\", (i, u, x, s, p))\n}\n"
	}
	at := "E0706@11:21"
	runBodyCases(t, []bodyCase{
		{"width on each kind", body("{i:5}{u:255}{x:3}{s:1}{p:9}"), ""},
		{"fill and align on each kind", body("{i:*<5}{u:>4}{x:^8}{s:_^7}{p:<3}"), ""},
		{"sign and zero on numbers", body("{i:+}{u:+06}{x:+08.2}{i:03}"), ""},
		{"radix on integers", body("{i:x}{i:X}{u:b}{u:o}{u:08x}{i:#>12b}"), ""},
		{"precision on a float", body("{x:.0}{x:.99}{x:+.3}"), ""},
		{"width 0", body("{s:00}"), at},
		{"width 256", body("{s:256}"), at},
		{"fill without align", body("{s:*5}"), at},
		{"align without width", body("{s:<}"), at},
		{"zero without width", body("{i:0}"), at},
		{"zero with align", body("{i:<05}"), at},
		{"sign with radix", body("{i:+x}"), at},
		{".N on an integer", body("{i:.2}"), at},
		{"radix on a float", body("{x:b}"), at},
		{"radix on a str", body("{s:X}"), at},
		{"sign on a str", body("{s:+}"), at},
		{"zero on a user Format", body("{p:05}"), at},
		{"? is refused", body("{p:?}"), at},
		{"# is refused", body("{i:#x}"), at},
		{"e is refused", body("{x:e}"), at},
		{"a fill byte outside printable ASCII", body("{s:\\t<5}"), at},
	})
	// A brace is no fill.
	runBodyCases(t, []bodyCase{
		{"{ as a fill", body("{s:{>5}"), at},
		{"} as a fill", body("{s:}>5}"), at},
		{"~ as a fill", body("{s:~>5}"), ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, at, body("{s:+}"))
	if d := b.bodyDiags(t)[0]; fact(d, "problem") != "+ needs a number field: s" {
		t.Fatalf("facts:\n%s", dump(w, b.bodyDiags(t)))
	}
}
