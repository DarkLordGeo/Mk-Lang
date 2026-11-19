// Why: a module loaded from the cache must give its generators' quotes the
// same import bindings as a whole load, or a warm build miscompiles silently.
// What: TestQuoteSelfNamedCoreGenerator to _REV4: Module.Imports lists the prelude and std.rt
// with no import line, so a loaded record's quote bindings come only from the
// import lines (LoadWith); Load, which has none, makes a quote an ICE.
// How: one module checked cold and warm through driverImports and withLines,
// and the token hash that keeps the cached import lines current.

package resolve

import (
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/syntax"
	"mk/internal/types"
)

// driverImports sets b's Module.Imports as the driver's setImports does for
// a module that sees the prelude and lies outside std.rt's closure: the
// written imports' modules, std.core and std.rt, each once, by path.
func driverImports(w *world, b *built, rt types.ModuleID) {
	ids := []types.ModuleID{w.core, rt}
	for _, f := range b.files {
		for _, il := range ImportsOf(f, w.in) {
			if dep, ok := w.mods[il.Path]; ok {
				ids = append(ids, dep.id)
			}
		}
	}
	slices.SortFunc(ids, func(x, y types.ModuleID) int {
		return strings.Compare(w.items.Module(x).Path, w.items.Module(y).Path)
	})
	w.items.Module(b.id).Imports = slices.Compact(ids)
}

// withLines loads b into s with its import lines, as the driver loads a
// module hit (frontcache.go).
func withLines(w *world) func(s *Session, b *built) {
	return func(s *Session, b *built) {
		files, imps := importLines(w, b)
		s.LoadWith(b.id, files, imps, nil)
	}
}

// quoteIsLoud reports whether a quote of generator module gen, emitted into
// a fresh module of session s, raises the ICE of a record Load made, which
// resolve's own Guard turns into an E0000.
func quoteIsLoud(t *testing.T, w *world, s *Session, path string, gen types.ModuleID, text string, words ...string) bool {
	t.Helper()
	old := w.s
	w.s = s
	b := w.add(path, opt{noSettle: true}, "struct User {}\n")
	w.s = old
	var ds, gs []*diag.Diagnostic
	if ie := source.Guard("quote", source.Span{}, func() { ds, gs = emit(b, text, literal(text, gen, words...)) }); ie != nil {
		return !ie.Runtime && strings.Contains(ie.What, "LoadWith")
	}
	for _, d := range append(ds, gs...) {
		if d.Code == diag.E0000 && strings.Contains(d.Message, "LoadWith") {
			return true
		}
	}
	t.Logf("%s: no ICE, diagnostics %v %v", path, ds, gs)
	return false
}

// TestQuoteSelfNamedCoreGenerator: a user generator module whose last segment is core (or
// rt), with no import of std.core (std.rt) written, names itself in its
// quote the std.json way. Module.Imports lists std.core and std.rt for it,
// so bindings guessed from them gave core = std.core warm and u.core cold,
// a silent miscompile. LoadWith gives the whole load's bindings, and Load
// is loud.
func TestQuoteSelfNamedCoreGenerator(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	rt := w.add("std.rt", opt{std: true}, "export struct Site {}\n")
	ucore := w.add("u.core", opt{}, "export struct Sip {}\nexport Make(g: int) { }\n")
	urt := w.add("u.rt", opt{}, "export struct Site {}\nexport Make(g: int) { }\n")
	for _, b := range []*built{ucore, urt} {
		driverImports(w, b, rt.id)
	}
	warm := warmSession(w, withLines(w), rt, ucore, urt)
	for _, c := range []struct {
		gen        *built
		text, want string
	}{
		{ucore, "F(a: core.Sip) { }\n", "core:module=u.core"},
		{urt, "F(a: rt.Site) { }\n", "rt:module=u.rt"},
	} {
		word := c.want[:strings.IndexByte(c.want, ':')]
		sameBindings(t, c.gen.path+" quote", w, warm.record(c.gen.id).quote, w.s.record(c.gen.id).quote)
		cold := quoteOutcome(t, w, w.s, c.gen.path+".cold", c.gen.id, c.text, word)
		got := quoteOutcome(t, w, warm, c.gen.path+".warm", c.gen.id, c.text, word)
		if !slices.Equal(cold, []string{c.want}) || !slices.Equal(got, cold) {
			t.Errorf("quote of %s\n cold %q\n warm %q\n want %q", c.gen.path, cold, got, c.want)
		}
		loaded := warmSession(w, func(s *Session, b *built) { s.Load(b.id) }, rt, c.gen)
		if !quoteIsLoud(t, w, loaded, c.gen.path+".load", c.gen.id, c.text, word) {
			t.Errorf("%s loaded by Load: its quote is not the ICE", c.gen.path)
		}
	}
}

// TestQuoteUnknownCoreWarmIsCold: a user generator module that imports a.lib and not
// std.core, whose quote writes core.X, is an unknown core cold. Module.Imports
// lists std.core for it, so guessed bindings made the warm check clean. With
// LoadWith the warm outcome is the cold one, the unknown core included, and
// with Load it is the ICE.
func TestQuoteUnknownCoreWarmIsCold(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	rt := w.add("std.rt", opt{std: true}, "export struct Site {}\n")
	lib := w.add("a.lib", opt{}, "export struct One {}\n")
	gen := w.add("x.gens", opt{}, "import a.lib\n\nexport Make(g: int) { }\n")
	driverImports(w, gen, rt.id)
	if got := len(w.items.Module(gen.id).Imports); got != 3 {
		t.Fatalf("x.gens Module.Imports has %d modules, want std.core, std.rt and a.lib", got)
	}
	text := "F(a: core.Duration, b: lib.One) { }\n"
	words := []string{"core", "lib"}
	warm := warmSession(w, withLines(w), rt, lib, gen)
	sameBindings(t, "x.gens quote", w, warm.record(gen.id).quote, w.s.record(gen.id).quote)
	cold := quoteOutcome(t, w, w.s, "cold", gen.id, text, words...)
	got := quoteOutcome(t, w, warm, "warm", gen.id, text, words...)
	if !slices.Equal(cold, got) {
		t.Errorf("quote of x.gens\n cold %q\n warm %q", cold, got)
	}
	if !slices.ContainsFunc(cold, func(s string) bool { return strings.HasPrefix(s, "E0201 ") && strings.Contains(s, "core") }) ||
		!slices.Contains(cold, "lib:module=a.lib") {
		t.Errorf("cold outcome %q, want an unknown core and lib = a.lib", cold)
	}
	loaded := warmSession(w, func(s *Session, b *built) { s.Load(b.id) }, rt, lib, gen)
	if !quoteIsLoud(t, w, loaded, "load", gen.id, text, words...) {
		t.Errorf("x.gens loaded by Load: its quote is not the ICE")
	}
}

// TestQuoteSwappedAliases: two aliased imports of modules that share a last segment
// (import m = a.lib, import l = b.lib), and the same lines with the aliases
// swapped: the quote's l and m resolve warm as cold, to b.lib and a.lib in
// the first module and the other way round in the second. Bindings named
// by the last segment made both `lib`, left out as bound twice.
func TestQuoteSwappedAliases(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	rt := w.add("std.rt", opt{std: true}, "export struct Site {}\n")
	alib := w.add("a.lib", opt{}, "export struct Two {}\nexport struct One {}\n")
	blib := w.add("b.lib", opt{}, "export struct One {}\nexport struct Two {}\n")
	pb := w.add("pb.gens", opt{}, "import m = a.lib\nimport l = b.lib\n\nexport Make(g: int) { }\n")
	sw := w.add("sw.gens", opt{}, "import l = a.lib\nimport m = b.lib\n\nexport Make(g: int) { }\n")
	for _, b := range []*built{pb, sw} {
		driverImports(w, b, rt.id)
	}
	warm := warmSession(w, withLines(w), rt, alib, blib, pb, sw)
	text := "F(a: l.One, b: m.Two) { }\n"
	for _, c := range []struct {
		gen  *built
		want []string
	}{
		{pb, []string{"l:module=b.lib", "m:module=a.lib"}},
		{sw, []string{"l:module=a.lib", "m:module=b.lib"}},
	} {
		sameBindings(t, c.gen.path+" quote", w, warm.record(c.gen.id).quote, w.s.record(c.gen.id).quote)
		cold := quoteOutcome(t, w, w.s, c.gen.path+".cold", c.gen.id, text, "l", "m")
		got := quoteOutcome(t, w, warm, c.gen.path+".warm", c.gen.id, text, "l", "m")
		if !slices.Equal(cold, c.want) || !slices.Equal(got, cold) {
			t.Errorf("quote of %s\n cold %q\n warm %q\n want %q", c.gen.path, cold, got, c.want)
		}
	}
}

// TestGeneratorImportEditMovesHash: what keeps a module hit's import lines current is each
// file's FileTokens.Outside in the module prefix, which the driver fills
// with the file anchor's token hash, syntax.FileHash of the whole file
// (frontcache.go, parsesum.go); FileTokens.Imports is never filled. Every
// edit of a generator module's import lines (add, remove, re-alias, swap
// two aliases, re-point) moves that hash, with the items unchanged, and a
// trivia-only edit does not.
func TestGeneratorImportEditMovesHash(t *testing.T) {
	t.Parallel()
	body := "\nexport Make(g: int) { }\nexport struct Own {}\n"
	variants := []struct{ name, imports string }{
		{"base", "import m = a.lib\nimport l = b.lib\n"},
		{"added", "import m = a.lib\nimport l = b.lib\nimport std.simd\n"},
		{"removed", "import m = a.lib\n"},
		{"realiased", "import n = a.lib\nimport l = b.lib\n"},
		{"swapped", "import l = a.lib\nimport m = b.lib\n"},
		{"repointed", "import m = a.lib\nimport l = c.lib\n"},
		{"unaliased", "import a.lib\nimport l = b.lib\n"},
	}
	in := source.NewInterner()
	sm := source.NewSourceMap()
	hash := func(name, text string) source.Hash {
		src := sm.Add("gens/"+name+".mk", []byte(text))
		f, ds := syntax.ParseFile(src, in, syntax.Options{})
		if len(ds) > 0 {
			t.Fatalf("%s: parse %v", name, ds[0].Message)
		}
		return syntax.FileHash(f, text)
	}
	seen := map[source.Hash]string{}
	for _, v := range variants {
		h := hash(v.name, v.imports+body)
		if prev, dup := seen[h]; dup {
			t.Errorf("%s and %s share the file token hash", prev, v.name)
		}
		seen[h] = v.name
	}
	if a, b := hash("trivia0", variants[0].imports+body), hash("trivia1", "// a comment\n"+variants[0].imports+body); a != b {
		t.Errorf("a comment moved the file token hash")
	}
}
