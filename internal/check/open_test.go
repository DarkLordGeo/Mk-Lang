// Why: an open set (an error set or enum that may gain members) must be
// matched outside its package with a `_` arm or a binding, so new members
// do not break callers silently.
// What: tests of open error sets (the future arm, capped witnesses, same-package sets, the
// word open in and away from its package).
// How: checks multi-module programs (openNet, openTls) and compares codes,
// reasons and notes.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// openStd is a std.fs with an open enum, an open error set and a closed
// enum.
const openStd = "export error NotFound\nexport error Denied\nexport open type Error = NotFound | Denied\nexport open enum Kind {\n\tFile\n\tDir\n}\nexport enum Mode {\n\tRead\n\tWrite\n}\n"

// TestOpenSetNeedsFutureArm: every shape a match can reach an open set
// in, and the shapes that cover its future members.
func TestOpenSetNeedsFutureArm(t *testing.T) {
	t.Parallel()
	const head = "import std.fs\nerror Mine\n"
	for _, c := range []struct {
		name, src, want string
		open            bool // the E0306 is the open one, with a proven fix
	}{
		{"enum, every variant", "F(k: fs.Kind) {\n\tmatch k {\n\t\t.File {\n\t\t}\n\t\t.Dir {\n\t\t}\n\t}\n}\n", "E0306@4:2", true},
		{"enum, value position", "F(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t\t.Dir { 2 }\n\t}\n}\n", "E0306@4:9", true},
		{"enum, every arm returns", "F(k: fs.Kind): int {\n\tmatch k {\n\t\t.File {\n\t\t\treturn 1\n\t\t}\n\t\t.Dir {\n\t\t\treturn 2\n\t\t}\n\t}\n}\n", "E0306@4:2", true},
		{"enum, _ arm", "F(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t\t_ { 2 }\n\t}\n}\n", "", false},
		{"enum, binding arm", "F(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t\tother { 2 }\n\t}\n}\n", "", false},
		{"enum, a variant missing", "F(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t}\n}\n", "E0306@4:9", false},
		{"closed std enum", "F(m: fs.Mode): int {\n\treturn match m {\n\t\t.Read { 1 }\n\t\t.Write { 2 }\n\t}\n}\n", "", false},
		{"error set, every member", "F(e: fs.Error): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tfs.Denied { 2 }\n\t}\n}\n", "E0306@4:9", true},
		{"error union, err per member", "F(r: fs.Error!int): int {\n\treturn match r {\n\t\tok(v) { v }\n\t\terr(fs.NotFound) { 1 }\n\t\terr(fs.Denied) { 2 }\n\t}\n}\n", "E0306@4:9", true},
		{"error union, err binding", "F(r: fs.Error!int): int {\n\treturn match r {\n\t\tok(v) { v }\n\t\terr(fs.NotFound) { 1 }\n\t\terr(e) { 2 }\n\t}\n}\n", "", false},
		{"optional enum", "F(o: ?fs.Kind): int {\n\treturn match o {\n\t\tsome(.File) { 1 }\n\t\tsome(.Dir) { 2 }\n\t\tnone { 3 }\n\t}\n}\n", "E0306@4:9", true},
		{"optional enum, some(_)", "F(o: ?fs.Kind): int {\n\treturn match o {\n\t\tsome(.File) { 1 }\n\t\tsome(_) { 2 }\n\t\tnone { 3 }\n\t}\n}\n", "", false},
		{"struct holding an enum", "struct P {\n\tk: fs.Kind\n\tb: bool\n}\nF(p: P): int {\n\treturn match p {\n\t\tP(k: .File) { 1 }\n\t\tP(k: .Dir) { 2 }\n\t}\n}\n", "E0306@8:9", true},
		// A set is open when it holds every member of an open alias:
		// one that names an open set, or holds its members among others, is
		// open, since the open set's growth reaches it.
		{"own set naming the open set", "type E = fs.Error | Mine\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tfs.Denied { 2 }\n\t\tMine { 3 }\n\t}\n}\n", "E0306@5:9", true},
		{"own set naming the open set, _ arm", "type E = fs.Error | Mine\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tMine { 3 }\n\t\t_ { 4 }\n\t}\n}\n", "", false},
		{"own set holding every member", "type E = fs.NotFound | fs.Denied | Mine\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tfs.Denied { 2 }\n\t\tMine { 3 }\n\t}\n}\n", "E0306@5:9", true},
		{"union naming the open set, err per member", "F(r: (fs.Error | Mine)!int): int {\n\treturn match r {\n\t\tok(v) { v }\n\t\terr(fs.NotFound) { 1 }\n\t\terr(fs.Denied) { 2 }\n\t\terr(Mine) { 3 }\n\t}\n}\n", "E0306@4:9", true},
		// std's allocating signatures, `Error | OutOfMemory!T` over an
		// open Error: a prelude member is of std too.
		{"open set and a prelude error", "type E = fs.Error | OutOfMemory\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tfs.Denied { 2 }\n\t\tOutOfMemory { 3 }\n\t}\n}\n", "E0306@5:9", true},
		// The types keep no spelling, so a set spelled member by member
		// with exactly the open set's members is that set.
		{"own set of exactly its members", "type E = fs.NotFound | fs.Denied\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tfs.Denied { 2 }\n\t}\n}\n", "E0306@5:9", true},
		{"own set holding part of it", "type E = fs.NotFound | Mine\nF(e: E): int {\n\treturn match e {\n\t\tfs.NotFound { 1 }\n\t\tMine { 3 }\n\t}\n}\n", "", false},
		// std.tls's shape: tls.Error is open and holds all of
		// net.Error, whose module path sorts first. The diagnostic, the fix's
		// Panic text and the stub case's note name the alias equal to the
		// set, then the one with the most members.
		{"open set holding a smaller open set", tlsHead + "F(e: tls.Error): int {\n\treturn match e {\n\t\ttls.Alert { 1 }\n\t\tnet.Refused { 2 }\n\t\tnet.Reset { 3 }\n\t}\n}\n", "E0306@5:9", true},
		{"open set holding a smaller open set, a member missing", tlsHead + "F(e: tls.Error): int {\n\treturn match e {\n\t\ttls.Alert { 1 }\n\t\tnet.Refused { 2 }\n\t}\n}\n", "E0306@5:9", false},
		{"own set holding both open sets", tlsHead + "F(e: tls.Error | Mine): int {\n\treturn match e {\n\t\ttls.Alert { 1 }\n\t\tnet.Refused { 2 }\n\t\tnet.Reset { 3 }\n\t\tMine { 4 }\n\t}\n}\n", "E0306@5:9", true},
		{"own set holding the smaller open set", tlsHead + "F(e: net.Error | Mine): int {\n\treturn match e {\n\t\tnet.Refused { 2 }\n\t\tnet.Reset { 3 }\n\t\tMine { 4 }\n\t}\n}\n", "E0306@5:9", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("std.fs", opt{std: true}, openStd)
			w.add("std.net", opt{std: true}, openNet)
			w.add("std.tls", opt{std: true}, openTls)
			src, set := head+c.src, "fs."
			if strings.HasPrefix(c.src, tlsHead) {
				src, set = c.src[len(tlsHead):], "tls.Error"
				if strings.Contains(c.src, "net.Error | Mine") {
					set = "net.Error"
				}
				src = "import std.net\nimport std.tls\nerror Mine\n" + src
			}
			b := checkBody(t, w, c.want, src)
			for _, d := range b.bodyDiags(t) {
				r, _ := d.FactValue("reason")
				if (r.Str == "open") != c.open {
					t.Errorf("reason %q, want open %v: %s", r.Str, c.open, dump(w, []*diag.Diagnostic{d}))
				}
				if c.open {
					if o, _ := d.FactValue("open"); !strings.HasPrefix(o.Str, set) {
						t.Errorf("open %q, want %s: %s", o.Str, set, dump(w, []*diag.Diagnostic{d}))
					}
				} else if set != "fs." {
					if n, _ := d.FactValue("note"); n.Str != set+" may gain members: keep a `_` arm" {
						t.Errorf("note %q, want it to name %s", n.Str, set)
					}
				}
				proven := 0
				for _, f := range d.Fixes {
					if f.Class == diag.Proven {
						proven++
						if len(f.Edits) != 1 || !strings.Contains(f.Edits[0].Text, "_ {") || !strings.Contains(f.Edits[0].Text, "Panic(\""+set) {
							t.Errorf("proven fix %+v", f)
						}
					}
				}
				if (proven == 1) != c.open {
					t.Errorf("%d proven fixes, want open %v", proven, c.open)
				}
			}
		})
	}
}

// openNet and openTls are a std.net and a std.tls of std's shape: tls's
// open Error holds net's open Error and an error of its own.
const (
	openNet = "export error Refused\nexport error Reset\nexport open type Error = Refused | Reset\n"
	openTls = "import std.net\nexport error Alert\nexport open type Error = Alert | net.Error\n"
	// tlsHead marks a case that imports std.net and std.tls in place of
	// the table's head; it is replaced, not compiled.
	tlsHead = "#tls\n"
)

// TestCappedFutureWitnessesHideNothing: when the witness bound is
// filled by future witnesses alone, a value of this release that is also
// unmatched still makes the E0306 the stub case, never the open one, whose
// proven `_` arm would swallow that value.
func TestCappedFutureWitnessesHideNothing(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("import std.fs\nenum C {\n")
	for i := 1; i <= 9; i++ {
		b.WriteString("\tV" + string(rune('0'+i)) + "\n")
	}
	b.WriteString("}\nstruct S {\n\tc: C\n\tk: fs.Kind\n}\nF(s: S): int {\n\treturn match s {\n")
	for i := 1; i <= 9; i++ {
		v := "V" + string(rune('0'+i))
		b.WriteString("\t\tS(c: ." + v + ", k: .File) { 1 }\n")
		if i < 9 {
			b.WriteString("\t\tS(c: ." + v + ", k: .Dir) { 2 }\n")
		}
	}
	b.WriteString("\t}\n}\n")
	w := newWorld(t, true)
	w.add("std.fs", opt{std: true}, openStd)
	m := checkBody(t, w, "E0306@18:9", b.String())
	for _, d := range m.bodyDiags(t) {
		if r, _ := d.FactValue("reason"); r.Str == "open" {
			t.Fatalf("reason open over a missing value of this release: %s", dump(w, []*diag.Diagnostic{d}))
		}
		if !strings.Contains(d.Message, "S(c: .V9, k: .Dir)") {
			t.Errorf("message %q does not name the missing value", d.Message)
		}
		for _, f := range d.Fixes {
			if f.Class == diag.Proven {
				t.Errorf("proven fix over a missing value: %+v", f)
			}
		}
	}
}

// TestSamePackageIsClosed: a module whose path shares the set's first
// segment matches it as a closed set: std's modules match std's sets, and a
// package's modules under one top directory match each other's. A module
// of another first segment needs the arm.
func TestSamePackageIsClosed(t *testing.T) {
	t.Parallel()
	const use = "F(k: a.Color): int {\n\treturn match k {\n\t\t.Red { 1 }\n\t\t.Blue { 2 }\n\t}\n}\n"
	for _, c := range []struct{ name, path, want string }{
		{"same top directory", "lib.b", ""},
		{"another top directory", "app", "E0306@3:9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("lib.a", opt{}, "export open enum Color {\n\tRed\n\tBlue\n}\n")
			b := w.add(c.path, opt{}, "import a = lib.a\n"+use)
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != c.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, c.want, dump(w, ds))
			}
		})
	}
	t.Run("std matches std", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		w.add("std.fs", opt{std: true}, openStd)
		b := w.add("std.http", opt{std: true}, "import std.fs\nexport F(k: fs.Kind): int {\n\treturn match k {\n\t\t.File { 1 }\n\t\t.Dir { 2 }\n\t}\n}\n")
		if ds := append(b.diags(), b.bodyDiags(t)...); len(ds) != 0 {
			t.Fatalf("std's own match: %s", dump(w, ds))
		}
	})
}

// TestOpenSetOfItsPackage: the word open is written only in the
// package that declares every member of the set, so an open alias over
// another package's errors is E0706 reason open-foreign at the word, and
// only in a module that declares one of them, so `sets.QOpen` over errs.A |
// errs.B is E0706 reason open-away; either opens nothing. Error sets are
// structural, so an exported open alias makes open, outside its package,
// every set that holds all its members: the alias itself, its twins
// (`errs.QErr`), and the unions that hold it, app's own included. Only the
// modules that declare a member are read, so the verdict is the same
// whether app imports q.sets, reaches it through a private import, or does
// not have it in its closure.
func TestOpenSetOfItsPackage(t *testing.T) {
	t.Parallel()
	const errs = "export error A\nexport error B\nexport type QErr = A | B\n"
	const use = "Direct(e: errs.QErr): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t}\n}\n"
	const sets = "import q.errs\nexport open type QOpen = errs.A | errs.B\n"
	const onSets = "OnOpen(e: sets.QOpen): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t}\n}\n"
	const x = "import q.errs\nexport open type X = errs.A | errs.B\n"
	// p imports e.x privately.
	const p = "import q.errs\nimport e.x\nexport error Mine\nHidden(v: x.X): int {\n\treturn 0\n}\n"
	// m imports q.sets privately.
	const m = "import q.sets\nexport error Mine\nHidden(v: sets.QOpen): int {\n\treturn 0\n}\n"
	// q.sets declares C and an open alias over errs.A and C, a member of
	// each of two modules of q.
	const twoMods = "import q.errs\nexport error C\nexport open type AC = errs.A | C\n"
	const onAC = "type M = errs.A | sets.C\nOnAC(e: M): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\tsets.C { 2 }\n\t}\n}\n"
	const away = "q.sets:E0706@2:8/open-away"
	// app's own union over q's errors, and a union of q's and e.x's.
	const own = "error Local\ntype Own = errs.QErr | Local\nOnOwn(e: Own): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t\tLocal { 3 }\n\t}\n}\n"
	const two = "type Two = errs.QErr | x.Y\nOnTwo(e: Two): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t\tx.Y { 3 }\n\t}\n}\n"
	// q.use, a module of q, matches a superset of q's open twin.
	const qUse = "import q.errs\nexport error U\ntype S = errs.A | errs.B | U\nF(e: S): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t\tU { 3 }\n\t}\n}\n"
	for _, c := range []struct {
		name, errs string
		mods       []string // path, text, in import order
		imp, tail  string
		want       string // app's diagnostics
		others     string // the other modules' diagnostics, "path:E0706@2:8/reason"
	}{
		{"a third package's open alias, imported", errs, []string{"e.x", x}, "import e.x\n", "U(v: x.X): int {\n\treturn 0\n}\n", "", "e.x:E0706@2:8/open-foreign"},
		{"a third package's open alias, matched", errs, []string{"e.x", x}, "import e.x\n", "OnX(e: x.X): int {\n\treturn match e {\n\t\terrs.A { 1 }\n\t\terrs.B { 2 }\n\t}\n}\n", "", "e.x:E0706@2:8/open-foreign"},
		{"a dependency's private import of one", errs, []string{"e.x", x, "p.p", p}, "import p.p\n", "V(v: p.Mine): int {\n\treturn 0\n}\n", "", "e.x:E0706@2:8/open-foreign"},
		{"q's own open twin", errs + "export open type Twin = A | B\n", nil, "", "", "E0306@3:9", ""},
		{"q's own open alias holding QErr", errs + "export error C\nexport open type Wide = A | B | C\n", nil, "", "", "", ""},
		// The same verdict whatever app's closure holds.
		{"another module of q, no member of its own, imported", errs, []string{"q.sets", sets}, "import q.sets\n", "U(v: sets.QOpen): int {\n\treturn 0\n}\n", "", away},
		{"another module of q, no member of its own, through a private import", errs, []string{"q.sets", sets, "m.m", m}, "import m.m\n", "V(v: m.Mine): int {\n\treturn 0\n}\n", "", away},
		{"another module of q, no member of its own, outside the imports", errs, []string{"q.sets", sets}, "", "", "", away},
		{"another module of q, no member of its own, matched", errs, []string{"q.sets", sets}, "import q.sets\n", onSets, "", away},
		{"another module of q, a private open alias", errs, []string{"q.sets", "import q.errs\nopen type QOpen = errs.A | errs.B\n"}, "import q.sets\n", "", "", "q.sets:E0706@2:1/open-away"},
		{"an open alias of q's errors and its own", errs, []string{"q.sets", "import q.errs\nexport error C\nexport open type QOpen = errs.A | errs.B | C\n"}, "import q.sets\n", "", "", ""},
		{"an open alias over two modules of q, matched", errs, []string{"q.sets", twoMods}, "import q.sets\n", onAC, "E0306@11:9", ""},
		{"a closed alias over two modules of q", errs, []string{"q.sets", strings.Replace(twoMods, "open ", "", 1)}, "import q.sets\n", onAC, "", ""},
		{"an open alias of two packages' errors", errs, []string{"e.x", "import q.errs\nexport error Mine\nexport open type X = errs.A | Mine\n"}, "import e.x\n", "", "", "e.x:E0706@3:8/open-foreign"},
		// A set that holds every member of an open alias is
		// open, whatever else it holds: app's own error, or another
		// package's. A module of q matches its own package's sets closed.
		{"app's own union over q's open twin", errs + "export open type Twin = A | B\n", nil, "", own, "E0306@11:9 E0306@3:9", ""},
		{"app's own union over q's closed set", errs, nil, "", own, "", ""},
		{"a union of two packages' errors, one open", errs + "export open type Twin = A | B\n", []string{"e.x", "export error Y\n"}, "import e.x\n", two, "E0306@11:9 E0306@4:9", ""},
		{"a union of two packages' errors, none open", errs, []string{"e.x", "export error Y\n"}, "import e.x\n", two, "", ""},
		{"q's own superset of its open twin", errs + "export open type Twin = A | B\n", []string{"q.use", qUse}, "", "", "E0306@3:9", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			var others []string
			q := w.add("q.errs", opt{}, c.errs)
			bs := []*built{q}
			for i := 0; i < len(c.mods); i += 2 {
				bs = append(bs, w.add(c.mods[i], opt{}, c.mods[i+1]))
			}
			b := w.add("app", opt{}, "import q.errs\n"+c.imp+use+c.tail)
			for _, o := range bs {
				for _, d := range append(o.diags(), o.bodyDiags(t)...) {
					if d.Code == diag.E0208 {
						continue // unused imports of short test modules
					}
					r, _ := d.FactValue("reason")
					others = append(others, o.path+":"+codes(w, []*diag.Diagnostic{d})+"/"+r.Str)
				}
			}
			if got := strings.Join(others, " "); got != c.others {
				t.Errorf("other modules' diagnostics %q, want %q", got, c.others)
			}
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != c.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, c.want, dump(w, ds))
			}
			for _, d := range ds {
				if r, _ := d.FactValue("reason"); r.Str != "open" {
					t.Errorf("reason %q, want open: %s", r.Str, dump(w, []*diag.Diagnostic{d}))
				}
			}
		})
	}
}

// TestOpenAwayWord: an open alias over errors of another module of
// its own package, with none of its own, is E0706 reason open-away at the
// word, exported or not; one member of its own module is enough.
func TestOpenAwayWord(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want string }{
		{"exported, no member of its own", "import lib.errs\nexport open type E = errs.A | errs.B\n", "E0706@2:8"},
		{"private, no member of its own", "import lib.errs\nopen type E = errs.A\n", "E0706@2:1"},
		{"one member of its own", "import lib.errs\nexport error C\nexport open type E = errs.A | C\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("lib.errs", opt{}, "export error A\nexport error B\n")
			b := w.add("lib.sets", opt{}, c.src)
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != c.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, c.want, dump(w, ds))
			}
			for _, d := range ds {
				if r, _ := d.FactValue("reason"); r.Str != "open-away" || !strings.Contains(d.Message, "declares a member of the set") {
					t.Errorf("reason %q: %s", r.Str, d.Message)
				}
			}
		})
	}
}

// TestOpenForeignWord: an open alias over std's errors outside std is
// E0706 reason open-foreign, and names the member of the other package.
func TestOpenForeignWord(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("std.fs", opt{std: true}, openStd)
	b := checkBody(t, w, "E0706@3:8", "import std.fs\nexport error Mine\nexport open type E = fs.NotFound | Mine\n")
	for _, d := range b.diags() {
		if r, _ := d.FactValue("reason"); r.Str != "open-foreign" || !strings.Contains(d.Message, "`fs.NotFound` is of package `std`") {
			t.Errorf("reason %q: %s", r.Str, d.Message)
		}
	}
}

// TestOpenWord: open is the modifier only before enum or type, an
// alias written open must be a non-generic error set (E0706), and open is
// a plain name elsewhere.
func TestOpenWord(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src, want string }{
		{"open enum and set", "error A\nerror B\nopen type E = A | B\nopen enum K {\n\tX\n}\n", ""},
		{"open alias of int", "open type N = int\n", "E0706@1:1"},
		{"open generic set", "error A\nopen type E[T] = A\n", "E0706@2:1"},
		{"open as a local", "F(): int {\n\topen := 1\n\treturn open\n}\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			checkBody(t, w, c.want, c.src)
		})
	}
}
