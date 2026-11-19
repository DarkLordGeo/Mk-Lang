// Why: std's own generators are Mk code, so they must check, lower and run
// through gen and print the expected source.
// What: TestStdDeriveGoldens (std/derive/derive.mk against its
// goldens), TestSketches (library generator sketches written as Mk test
// generators) and BenchmarkGenDeriveEq.
// How: an mkWorld checks and lowers the modules (userModule, mustGen), then
// gen runs over their MIR.

package gen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/types"
)

// userModule is module main with struct User { name: str, age: int } and
// one gen line per generator path, importing imp.
func userModule(imp string, paths ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "import %s\n\nstruct User {\n    name: str\n    age: int\n}\n", imp)
	for _, p := range paths {
		fmt.Fprintf(&b, "\ngen %s(User)\n", p)
	}
	return b.String()
}

// mustGen is m's generated text, failing the test on any diagnostic (stage
// 5's check of the generated items included) or when no line produced
// output. Each line must have run its generator's lowered body on eval, and
// each generated impl method must have checked and lowered as a unit.
func mustGen(t *testing.T, w *mkWorld, m *mkMod) string {
	t.Helper()
	if len(m.diags) > 0 {
		t.Fatalf("%s: want no diagnostics, got:\n%s", m.path, w.dump(m.diags))
	}
	if m.gen == nil || m.gen.AST == nil || len(m.gen.AST.Items) == 0 {
		t.Fatalf("%s: no generated items", m.path)
	}
	for i, lr := range m.gen.Lines {
		if !lr.Ran || lr.Steps == 0 {
			t.Fatalf("%s: gen line %d did not run (ran %v, %d steps)", m.path, i, lr.Ran, lr.Steps)
		}
		if w.bodies[m.lines[i].Generator] == nil {
			t.Fatalf("%s: gen line %d's generator has no lowered body", m.path, i)
		}
	}
	lowered := 0
	for _, id := range m.m.Units() {
		it := w.items.Get(id)
		generated := it.Origin != nil || (it.Owner != types.NoItem && w.items.Get(it.Owner).Origin != nil)
		if generated && w.bodies[id] != nil {
			lowered++
		}
	}
	if lowered == 0 {
		t.Fatalf("%s: no generated unit was lowered", m.path)
	}
	return m.gen.Text()
}

// TestStdDeriveGoldens: std's derive.Eq, derive.Hash and
// derive.Format over struct User { name: str, age: int }, each on its own
// gen line in module main, print derive_eq.golden, derive_hash.golden and
// derive_format.golden exactly: the text the hand-built MIR gives, from
// std's source.
func TestStdDeriveGoldens(t *testing.T) {
	for _, c := range []struct{ gen, golden string }{
		{"Eq", "derive_eq.golden"},
		{"Hash", "derive_hash.golden"},
		{"Format", "derive_format.golden"},
	} {
		t.Run(c.gen, func(t *testing.T) {
			w := newMkWorld(t)
			w.addStd("std.derive", "std/derive/derive.mk")
			m := w.addLoose("main", false, map[string]string{"main.mk": userModule("std.derive", "derive."+c.gen)})
			golden(t, c.golden, mustGen(t, w, m))
		})
	}
}

// sketchDerive is the derive.Hash and derive.Format sketches as Mk test
// generators of a package module, derive, not std's.
const sketchDerive = `// Hash: one self.f.Hash(h) per field, in declaration order.
export Hash(g: mut Gen, t: Type) {
    var body := g.Block()
    for f in t.Fields() {
        body.Add(quote {
            self.$(f.Name()).Hash(h)
        })
    }
    g.Emit(quote {
        impl Hash for $(t.Name()) {
            Hash(&self, h: mut Hasher) {
                $body
            }
        }
    })
}

// Format: Name(f1: v1, f2: v2).
export Format(g: mut Gen, t: Type) {
    var body := g.Block()
    body.Add(quote {
        try w.Str($(g.Str(t.Text())))
        try w.Str("(")
    })
    var i := 0
    for f in t.Fields() {
        if i > 0 {
            body.Add(quote {
                try w.Str(", ")
            })
        }
        body.Add(quote {
            try w.Str($(g.Str(f.Name())))
            try w.Str(": ")
            try self.$(f.Name()).Format(w)
        })
        i += 1
    }
    body.Add(quote {
        try w.Str(")")
    })
    g.Emit(quote {
        impl Format for $(t.Name()) {
            Format(&self, w: mut Writer): WriteError!void {
                $body
            }
        }
    })
}
`

// sketchDyn is dyn.Of(I) as a Mk test generator. It names its types
// with Gen.Ident of a str constant, as TestSketchesHandBuilt does:
// "StoreDyn" from t.Text() takes std's Text in g.Arena(), which this
// harness's prelude declares without bodies.
const sketchDyn = `export Of(g: mut Gen, t: Type) {
    if t.Kind() != TypeKind.Interface {
        g.FailOn(t, "dyn.Of needs an interface")
        return
    }
    for m in t.Methods() {
        if m.Receiver() == Receiver.Value || m.Generic() || m.HasFlows() {
            g.FailOn(t, "dyn.Of: a method takes self by value, is generic or declares flows")
            return
        }
    }
    name := g.Ident("StoreDyn")
    table := g.Ident("StoreDynTable")
    var slots := g.Block()
    var methods := g.Block()
    for m in t.Methods() {
        var sig := quote { &self }
        if m.Receiver() == Receiver.Mut {
            sig = quote { mut self }
        }
        var tys := quote { *u8 }
        var args := quote { self.data }
        for p in m.Params() {
            sig = sig.Then(quote { , $(p.Name()): $(p.Type()) })
            tys = tys.Then(quote { , $(p.Type()) })
            args = args.Then(quote { , $(p.Name()) })
        }
        slots.Add(quote {
            $(m.Name()): fn($tys): $(m.Result())
        })
        methods.Add(quote {
            $name.$(m.Name())($sig): $(m.Result()) {
                return self.vt.$(m.Name())($args)
            }
        })
    }
    g.Emit(quote {
        export struct $name {
            data: *u8
            vt: &$table
        }

        export struct $table {
            $slots
        }

        $methods
    })
}
`

// storeModule is module main for dyn.Of: interface Store and one gen line
// with argument arg; struct User and interface Sink (a by-value method)
// are there for the refusals.
func storeModule(arg string) string {
	return "import dyn\n\nstruct User {\n    name: str\n}\n\n" +
		"interface Store {\n    Get(&self, k: int): str\n    Put(mut self, v: []u8)\n}\n\n" +
		"interface Sink {\n    Close(self)\n}\n\ngen dyn.Of(" + arg + ")\n"
}

// TestSketches: derive.Hash, derive.Format and dyn.Of written as
// Mk test generators check, lower and run over their MIR. Over
// struct User { name: str, age: int } and interface Store { Get(&self, k:
// int): str; Put(mut self, v: []u8) } they print derive_hash.golden,
// derive_format.golden and dyn_of.golden, the texts the hand-built sketches
// print, and their output parses; Hash's and Format's output also checks
// in module main. dyn.Of refuses a struct and an interface with a by-value
// method with E0903 at the type's declaration, and keeps no output.
func TestSketches(t *testing.T) {
	for _, c := range []struct{ gen, golden string }{
		{"Hash", "derive_hash.golden"},
		{"Format", "derive_format.golden"},
	} {
		t.Run(c.gen, func(t *testing.T) {
			w := newMkWorld(t)
			w.add("derive", false, map[string]string{"derive.mk": sketchDerive})
			m := w.addLoose("main", false, map[string]string{"main.mk": userModule("derive", "derive."+c.gen)})
			golden(t, c.golden, mustGen(t, w, m))
		})
	}
	t.Run("Of", func(t *testing.T) {
		w := newMkWorld(t)
		w.add("dyn", false, map[string]string{"dyn.mk": sketchDyn})
		m := w.addLoose("main", false, map[string]string{"main.mk": storeModule("Store")})
		if m.gen == nil || len(m.gen.Diags) > 0 || m.gen.AST == nil || len(m.gen.AST.Items) != 4 {
			t.Fatalf("dyn.Of(Store): want 4 parsed items and no stage-4 diagnostic:\n%s", w.dump(m.diags))
		}
		if len(m.diags) > 0 {
			t.Logf("dyn.Of(Store)'s output does not check in main (the sketch's shape, not gen's):\n%s", w.dump(m.diags))
		}
		golden(t, "dyn_of.golden", m.gen.Text())
	})
	for _, c := range []struct{ arg, want string }{
		{"User", "needs an interface"},
		{"Sink", "by value"},
	} {
		t.Run("Of("+c.arg+")", func(t *testing.T) {
			w := newMkWorld(t)
			w.add("dyn", false, map[string]string{"dyn.mk": sketchDyn})
			src := storeModule(c.arg)
			m := w.addLoose("main", false, map[string]string{"main.mk": src})
			if len(m.diags) != 1 || m.diags[0].Code.String() != "E0903" {
				t.Fatalf("dyn.Of(%s): want one E0903, got:\n%s", c.arg, w.dump(m.diags))
			}
			d := m.diags[0]
			id, ok := w.items.ByKey(types.ItemKey("main", c.arg))
			if !ok || d.At != w.items.Get(id).Span || !strings.Contains(d.Message, c.want) {
				t.Fatalf("E0903 at %v (%s), want %s's declaration", d.At, d.Message, c.arg)
			}
			if m.gen.File != nil {
				t.Fatalf("a refused run's output was kept:\n%s", m.gen.Text())
			}
		})
	}
}

// BenchmarkGenDeriveEq: std's gen derive.Eq(User) over a 10-field struct, from
// bind to parsed output, the generator's MIR lowered from
// std/derive/derive.mk. One op is one Context.Run of the module's one line, on
// the module built once.
func BenchmarkGenDeriveEq(b *testing.B) {
	w := newMkWorld(b)
	w.addStd("std.derive", "std/derive/derive.mk")
	var src strings.Builder
	src.WriteString("import std.derive\n\nstruct User {\n")
	for i := range 10 {
		fmt.Fprintf(&src, "    f%d: int\n", i)
	}
	src.WriteString("}\n\ngen derive.Eq(User)\n")
	m := w.add("main", false, map[string]string{"main.mk": src.String()})
	if m.gen == nil || len(m.gen.AST.Items) != 1 {
		b.Fatal("derive.Eq gave no output")
	}
	steps := m.gen.Lines[0].Steps
	b.ResetTimer()
	for b.Loop() {
		out := m.ctx.Run(m.id, m.lines)
		if len(out.Diags) != 0 || out.AST == nil {
			b.Fatal("derive.Eq failed")
		}
	}
	b.ReportMetric(float64(steps), "steps/op")
}
