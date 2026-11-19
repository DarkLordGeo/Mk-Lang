// Why: generated code lands in other modules, so a literal name in a quote
// must resolve the same wherever the output goes.
// What: tests that a literal name in a quote names the generator's own module
// by the last segment of its path, unless one of the module's imports takes
// that name, and through it only the module's exported items; a bare literal
// name never sees them.
// How: a jsonGen module shaped like std/json, with quote output fed through
// AddGenerated and Settle.

package resolve

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// jsonGen is a generator module std.json as std/json is: it exports Value,
// Json and the generator, and keeps Hidden private.
const jsonGen = "export Codec(g: int)\nexport struct Value {}\nexport interface Json {}\nstruct Hidden {}\n"

// literal makes every occurrence of each word in text a NameLiteral token of
// generator module gen, as gen's quote tags every name it writes.
func literal(text string, gen types.ModuleID, words ...string) []NameOrigin {
	var out []NameOrigin
	for _, w := range words {
		for i := 0; ; {
			j := strings.Index(text[i:], w)
			if j < 0 {
				break
			}
			out = append(out, NameOrigin{Offset: uint32(i + j), Kind: NameLiteral, Module: gen})
			i += j + len(w)
		}
	}
	return out
}

// genRef is the Ref of the n-th ident spelled name in b's generated files.
func genRef(t *testing.T, b *built, name string, n int) Ref {
	t.Helper()
	for _, fi := range b.info.Files {
		if !fi.Generated {
			continue
		}
		var found []*ast.Ident
		ast.Inspect(fi.File, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok && b.in.Str(id.Name) == name {
				found = append(found, id)
			}
			return x != nil
		})
		if n < len(found) {
			return fi.Refs[found[n].ID()]
		}
		n -= len(found)
	}
	t.Fatalf("%s: no generated ident %q #%d", b.path, name, n)
	return Ref{}
}

// emit runs one generated file through AddGenerated and Settle and returns
// Settle's diagnostics, handwritten and generated.
func emit(b *built, text string, names []NameOrigin) (ds, gs []*diag.Diagnostic) {
	gf := b.generate(text)
	gf.Names = names
	b.m.AddGenerated([]GeneratedFile{gf})
	return b.m.Settle()
}

func TestOwnModuleNameInQuoteResolves(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	gen := w.add("std.json", opt{std: true}, jsonGen)
	// The output module binds std.json as j, so nothing in it is named json:
	// the generated code needs no import where it lands.
	b := w.add("main", opt{noSettle: true}, "import j = std.json\n\nstruct User {}\ngen j.Codec(User)\n")
	text := "impl json.Json for User {}\nF(v: &json.Value) { }\n"
	ds, gs := emit(b, text, literal(text, gen.id, "json", "Json", "Value", "F", "v"))
	if len(ds) != 0 || len(gs) != 0 {
		t.Fatalf("diags %v, generated %v\n%s", ds, gs, b.dump())
	}
	for n := range 2 {
		if r := genRef(t, b, "json", n); r.Kind != RefModule || types.ModuleID(r.A) != gen.id {
			t.Errorf("json #%d: %+v, want the generator's module", n, r)
		}
	}
	if r := genRef(t, b, "Value", 0); r.Kind != RefItem || types.ItemID(r.A) != w.item("std.json.Value").ID {
		t.Errorf("json.Value: %+v", r)
	}
	if r := genRef(t, b, "Json", 0); r.Kind != RefItem || types.ItemID(r.A) != w.item("std.json.Json").ID {
		t.Errorf("json.Json: %+v", r)
	}
	// The impl's key spells its head through the generator's module, as a
	// handwritten `impl j.Json for User` has it.
	key := types.ImplKey("main", w.item("std.json.Json").Key.String(), w.item("main.User").Key.String())
	if _, ok := w.items.ByKey(key); !ok {
		t.Errorf("no impl %s\n%s", key, b.dump())
	}
	noPending(t, b)
}

func TestBareOwnItemInQuoteIsUnknown(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	gen := w.add("std.json", opt{std: true}, jsonGen)
	b := w.add("main", opt{noSettle: true}, "import std.json\n\nstruct User {}\ngen json.Codec(User)\n")
	text := "F(v: Value) { _ = json }\n"
	ds, gs := emit(b, text, literal(text, gen.id, "Value", "json", "F", "v"))
	if len(ds) != 0 || len(gs) != 2 || gs[0].Code != diag.E0201 || gs[1].Code != diag.E0201 {
		t.Fatalf("diags %v, generated %v", ds, gs)
	}
	// Order is not by position: json is found (a module, not a value) when
	// the file is added, Value fails only when Settle retries it.
	msgs := gs[0].Message + " / " + gs[1].Message
	if !strings.Contains(msgs, "unknown name `Value`") || !strings.Contains(msgs, "`json` is a module, not a value") {
		t.Errorf("messages %q", msgs)
	}
	if r := genRef(t, b, "Value", 0); r.Kind != RefPoison {
		t.Errorf("bare Value: %+v", r)
	}
}

func TestImportTakesTheName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, imp, dep, want string
	}{
		{"same last segment", "import other.json\n\n", "other.json", "other.json.Value"},
		{"alias", "import json = lib\n\n", "lib", "lib.Value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			dep := w.add(tc.dep, opt{}, "export struct Value {}\n")
			gen := w.add("std.json", opt{std: true}, tc.imp+jsonGen+"Use(v: json.Value) { }\n")
			gen.want(t)
			b := w.add("main", opt{noSettle: true}, "import j = std.json\n\nstruct User {}\ngen j.Codec(User)\n")
			text := "F(v: json.Value) { }\n"
			ds, gs := emit(b, text, literal(text, gen.id, "json", "Value", "F", "v"))
			if len(ds) != 0 || len(gs) != 0 {
				t.Fatalf("diags %v, generated %v", ds, gs)
			}
			if r := genRef(t, b, "json", 0); r.Kind != RefModule || types.ModuleID(r.A) != dep.id {
				t.Errorf("json: %+v, want %s", r, tc.dep)
			}
			if r := genRef(t, b, "Value", 0); r.Kind != RefItem || types.ItemID(r.A) != w.item(tc.want).ID {
				t.Errorf("json.Value: %+v, want %s", r, tc.want)
			}
		})
	}
}

func TestAmbiguousImportStillTakesTheName(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	w.add("x.json", opt{}, "export struct A {}\n")
	w.add("y.json", opt{}, "export struct A {}\n")
	gen := w.add("std.json", opt{}, "import x.json\n\nUseA(a: json.A) { }\n", "import y.json\n\nUseB(a: json.A) { }\n")
	if len(gen.m.rec.quote) != 0 {
		t.Errorf("an import bound json in some file, yet quotes see %+v", gen.m.rec.quote)
	}
}

func TestOnlyExportedItemsReachable(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	gen := w.add("std.json", opt{std: true}, jsonGen)
	b := w.add("main", opt{noSettle: true}, "import std.json\n\nstruct User {}\ngen json.Codec(User)\n")
	text := "F(v: json.Hidden) { }\n"
	ds, gs := emit(b, text, literal(text, gen.id, "json", "Hidden", "F", "v"))
	if len(ds) != 0 || len(gs) != 1 || gs[0].Code != diag.E0204 {
		t.Fatalf("diags %v, generated %v", ds, gs)
	}
	if fact(gs[0], "module") != "std.json" {
		t.Errorf("%s", b.dump())
	}
}

func TestHandwrittenCodeDoesNotSeeTheName(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	// No Json here: `json` would fold to it (E0207).
	b := w.add("std.json", opt{std: true}, "export Codec(g: int)\nexport struct Value {}\nF(v: json.Value) { }\n")
	b.want(t, "E0201@3:6")
}

func TestLoadedGeneratorNamesItself(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	other := w.add("other.json", opt{}, "export struct Value {}\n")
	gen := w.add("std.json", opt{std: true}, jsonGen)
	taken := w.add("std.x.json", opt{std: true}, "import other.json\n\nexport struct Value {}\nUse(v: json.Value) { }\n")
	w.items.Module(taken.id).Imports = []types.ModuleID{other.id}
	s2 := NewSession(w.cfg)
	for _, m := range []*built{gen, taken} {
		files, imps := importLines(w, m)
		s2.LoadWith(m.id, files, imps, nil)
	}
	if q := s2.record(gen.id).quote; len(q) != 1 || q[0].Module != gen.id || w.in.Str(q[0].Name) != "json" {
		t.Errorf("loaded std.json's quote bindings %+v", q)
	}
	// As a whole load: the import of other.json is the quotes' `json`, and
	// the module's own name is taken by it (it had no binding).
	if q := s2.record(taken.id).quote; len(q) != 1 || q[0].Module != other.id {
		t.Errorf("loaded std.x.json imports other.json, yet quotes see %+v", q)
	}
}

// TestImportUsedOnlyInQuote: an import that only the module's quotes
// name is used, at the generator module's own resolution; one named nowhere,
// or only after a `.` in a quote, is still E0208.
func TestImportUsedOnlyInQuote(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	w.add("lib", opt{}, "export struct Thing {}\n")
	w.add("other", opt{}, "export struct Thing {}\n")
	w.add("member", opt{}, "export struct Thing {}\n")
	b := w.add("std.gen1", opt{std: true}, "import lib\nimport other\nimport member\n\n"+
		"export Eq(g: int) {\n    _ = quote { F(x: lib.Thing, y: v.member) { } }\n}\n")
	b.want(t, "E0208@2:1", "E0208@3:1")
	// The quote's root resolves through the union of the files' bindings:
	// another file's import is used by it when the quote's file has none.
	c := w.add("std.gen2", opt{std: true}, "import lib\n\nexport struct Own {}\n",
		"export Eq(g: int) {\n    _ = quote { F(x: lib.Thing) { } }\n}\n")
	c.want(t)
}
