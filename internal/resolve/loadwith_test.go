// Why: a warm build must resolve a gen line's output as the cold build did,
// so a module loaded from the cache needs its generators' import bindings.
// What: tests that Session.LoadWith gives a module's quotes the bindings a
// whole load (Begin) gives them, std.regex's core, raw and simd included.
// How: a warmSession loads modules from their items and compares bindings and
// each quoted word's resolution with the cold session.

package resolve

import (
	"slices"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// importLines is ImportsOf of each of b's files, and the import map Begin
// was given for them: what the driver passes LoadWith on a module hit.
func importLines(w *world, b *built) ([][]ImportLine, map[string]types.ModuleID) {
	var files [][]ImportLine
	imps := map[string]types.ModuleID{}
	for _, f := range b.files {
		ls := ImportsOf(f, w.in)
		for _, il := range ls {
			if dep, ok := w.mods[il.Path]; ok {
				imps[il.Path] = dep.id
			}
		}
		files = append(files, ls)
	}
	return files, imps
}

// sameBindings compares two binding lists by name, module and path, in order.
func sameBindings(t *testing.T, what string, w *world, got, want []Binding) {
	t.Helper()
	str := func(bs []Binding) []string {
		var out []string
		for _, b := range bs {
			out = append(out, w.in.Str(b.Name)+"="+b.Path)
			if b.Module == types.NoModule {
				out[len(out)-1] += "(poisoned)"
			}
		}
		return out
	}
	if !slices.Equal(str(got), str(want)) {
		t.Errorf("%s: loaded %v, whole load %v", what, str(got), str(want))
	}
}

// warmSession is a second session over w's Items in which every module of
// mods is loaded from its items, as the driver loads module hits, by load.
func warmSession(w *world, load func(s *Session, b *built), mods ...*built) *Session {
	s := NewSession(w.cfg)
	if w.core != types.NoModule {
		s.Load(w.core)
	}
	for _, b := range mods {
		load(s, b)
	}
	return s
}

// quoteOutcome emits text into a fresh module of session s that holds a gen
// line, every word of words a NameLiteral token of generator module gen, and
// renders the generated diagnostics and each word's first resolution.
func quoteOutcome(t *testing.T, w *world, s *Session, path string, gen types.ModuleID, text string, words ...string) []string {
	t.Helper()
	old := w.s
	w.s = s
	b := w.add(path, opt{noSettle: true}, "struct User {}\n")
	w.s = old
	ds, gs := emit(b, text, literal(text, gen, words...))
	var out []string
	for _, d := range append(ds, gs...) {
		out = append(out, d.Code.String()+" "+d.Message)
	}
	for _, word := range words {
		r := genRef(t, b, word, 0)
		out = append(out, word+":"+r.Kind.String())
		switch r.Kind {
		case RefModule:
			out[len(out)-1] += "=" + w.items.Module(types.ModuleID(r.A)).Path
		case RefItem:
			out[len(out)-1] += "=" + w.items.Get(types.ItemID(r.A)).Key.String()
		}
	}
	return out
}

// TestLoadWithQuoteMatchesWholeLoad: LoadWith binds a generator
// module's quotes exactly as Begin does, an alias, a name bound to two
// modules across files (left out, still taken) and an alias that takes the
// module's own name included; a quote's literal names then resolve
// the same in the warm session as in the cold one.
func TestLoadWithQuoteMatchesWholeLoad(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	raw := w.add("x.raw", opt{}, "export struct R {}\n")
	simd := w.add("y.simd", opt{}, "export struct V {}\n")
	amb1 := w.add("p.amb", opt{}, "export struct A {}\n")
	amb2 := w.add("q.amb", opt{}, "export struct A {}\n")
	gen := w.add("g.regex", opt{std: true},
		"import x.raw\nimport v = y.simd\nimport p.amb\n\nexport Matcher(g: int)\nexport struct Own {}\n",
		"import x.raw\nimport q.amb\n\nexport struct Two {}\n")
	// An alias takes the module's own name: its quotes' `json` is x.raw.
	taken := w.add("h.json", opt{std: true}, "import json = x.raw\n\nexport Codec(g: int)\nexport struct Value {}\n")
	deps := []*built{raw, simd, amb1, amb2}

	s2 := warmSession(w, func(s *Session, b *built) {
		files, imps := importLines(w, b)
		s.LoadWith(b.id, files, imps, nil)
	}, append(deps, gen, taken)...)
	for _, b := range []*built{gen, taken} {
		sameBindings(t, b.path+" lit", w, s2.record(b.id).lit, w.s.record(b.id).lit)
		sameBindings(t, b.path+" quote", w, s2.record(b.id).quote, w.s.record(b.id).quote)
	}
	if q := s2.record(taken.id).quote; len(q) != 1 || q[0].Module != raw.id {
		t.Errorf("h.json's alias json = x.raw: loaded quotes see %+v", q)
	}

	text := "F(a: raw.R, b: v.V, c: regex.Own, d: amb.A) { }\n"
	words := []string{"raw", "v", "regex", "amb"}
	cold := quoteOutcome(t, w, w.s, "cold", gen.id, text, words...)
	warm := quoteOutcome(t, w, s2, "warm", gen.id, text, words...)
	if !slices.Equal(cold, warm) {
		t.Errorf("quote of g.regex\n cold %q\n warm %q", cold, warm)
	}
	// amb is bound to two modules: unknown in both, the rest resolve.
	if len(cold) == 0 || !slices.Contains(cold, "raw:module=x.raw") || !slices.Contains(cold, "v:module=y.simd") ||
		!slices.Contains(cold, "regex:module=g.regex") {
		t.Errorf("cold outcome %q", cold)
	}
	jtext := "G(a: json.R) { }\n"
	jcold := quoteOutcome(t, w, w.s, "jcold", taken.id, jtext, "json")
	jwarm := quoteOutcome(t, w, s2, "jwarm", taken.id, jtext, "json")
	if !slices.Equal(jcold, jwarm) || !slices.Contains(jcold, "json:module=x.raw") {
		t.Errorf("quote of h.json\n cold %q\n warm %q", jcold, jwarm)
	}
}

// TestLoadStdGeneratorQuote is the bug: a std generator module that
// writes `import std.core`, `std.raw` and `std.simd` and no alias, loaded
// by LoadWith (its import lines, as the driver loads a module hit), gives
// its quotes core, raw and simd as a whole load does, so the generated code
// resolves with no E0201 (it was "unknown name `core`" on a warm check).
func TestLoadStdGeneratorQuote(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	raw := w.add("std.raw", opt{std: true, noPrelude: true}, "export struct Ptr {}\n")
	simd := w.add("std.simd", opt{std: true}, "export struct V16 {}\n")
	gen := w.add("std.regex", opt{std: true},
		"import std.core\nimport std.raw\nimport std.simd\n\nexport Matcher(g: int)\nexport struct Match {}\n")
	// The driver's Module.Imports: direct imports by path.
	w.items.Module(gen.id).Imports = []types.ModuleID{w.core, raw.id, simd.id}
	s2 := warmSession(w, func(s *Session, b *built) {
		files, imps := importLines(w, b)
		s.LoadWith(b.id, files, imps, nil)
	}, raw, simd, gen)
	sameBindings(t, "std.regex quote", w, s2.record(gen.id).quote, w.s.record(gen.id).quote)

	text := "F(a: core.Duration, b: raw.Ptr, c: simd.V16, d: regex.Match) { }\n"
	words := []string{"core", "raw", "simd", "regex"}
	cold := quoteOutcome(t, w, w.s, "cold", gen.id, text, words...)
	warm := quoteOutcome(t, w, s2, "warm", gen.id, text, words...)
	want := []string{"core:module=std.core", "raw:module=std.raw", "simd:module=std.simd", "regex:module=std.regex"}
	if !slices.Equal(cold, want) || !slices.Equal(warm, want) {
		t.Errorf("quote of std.regex\n cold %q\n warm %q\n want %q", cold, warm, want)
	}
}

// TestImportEditChangesBindings: the bindings LoadWith gives are a
// function of the import lines, (alias, path) per file, which each file's
// token hash covers in the module's cache prefix (FileTokens.Outside,
// TestGeneratorImportEditMovesHash): adding, removing or re-aliasing an import changes them,
// so a cache keyed on the old lines must not serve the edited module.
func TestImportEditChangesBindings(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("x.raw", opt{}, "export struct R {}\n")
	w.add("y.simd", opt{}, "export struct V {}\n")
	base := "export Matcher(g: int)\n"
	variants := []struct{ name, imports string }{
		{"base", "import x.raw\n\n"},
		{"added", "import x.raw\nimport y.simd\n\n"},
		{"removed", ""},
		{"aliased", "import r = x.raw\n\n"},
		{"realiased", "import q = x.raw\n\n"},
	}
	seen := map[string]string{}
	for i, v := range variants {
		b := w.add("g"+string(rune('a'+i))+".gen", opt{std: true}, v.imports+base)
		files, imps := importLines(w, b)
		s := NewSession(w.cfg)
		s.LoadWith(b.id, files, imps, nil)
		var key string
		for _, bd := range s.record(b.id).lit {
			key += w.in.Str(bd.Name) + "=" + bd.Path + ";"
		}
		if prev, dup := seen[key]; dup {
			t.Errorf("%s and %s give the same quote bindings %q", prev, v.name, key)
		}
		seen[key] = v.name
	}
}

// TestLoadWithImplicitAndDuplicate: an implicit binding (pkg.mk's
// `pkg`) is a quote binding of a loaded module as of a whole one, and a
// second import line whose name folds to an earlier one binds nothing.
func TestLoadWithImplicitAndDuplicate(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	pkg := w.add("std.pkg", opt{std: true}, "export struct Bin {}\n")
	w.add("x.raw", opt{}, "export struct R {}\n")
	w.add("y.raw", opt{}, "export struct R {}\n")
	implicit := []Implicit{{Name: w.in.Intern("pkg"), Module: pkg.id}}
	// Begin with the implicit binding, as the driver gives a manifest module.
	b := w.begin("g.gen", opt{std: true}, "import x.raw\nimport y.raw\n\nexport Matcher(g: int)\n")
	files, imps := importLines(w, b)
	cold := NewSession(w.cfg)
	id := w.items.AddModule(&types.Module{Path: "h.gen", Files: w.items.Module(b.id).Files})
	m := cold.Begin(&ModuleInput{Module: id, Files: b.files, Imports: imps, Implicit: implicit})
	if !slices.ContainsFunc(m.Finish(1).Diags, func(d *diag.Diagnostic) bool { return d.Code == diag.E0206 }) {
		t.Errorf("no E0206 for the duplicate raw")
	}
	warm := NewSession(w.cfg)
	warm.LoadWith(id, files, imps, implicit)
	sameBindings(t, "lit", w, warm.record(id).lit, cold.record(id).lit)
	sameBindings(t, "quote", w, warm.record(id).quote, cold.record(id).quote)
	if lit := warm.record(id).lit; len(lit) != 2 || w.in.Str(lit[0].Name) != "pkg" || lit[1].Path != "x.raw" {
		t.Errorf("lit %+v", lit)
	}
}
