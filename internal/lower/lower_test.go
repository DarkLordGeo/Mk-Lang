// Why: the base helpers and first tests of the lower package.
// What: lowerModule, lowerSrc and wantIn, used by most tests here, and
// TestEmptyBodies, TestSlice, TestShowSample.
// How: bodies are built from Mk source through the front end
// (harness_test.go), lowered with Validate on, and checked by their printed
// MIR.

package lower

import (
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// lowered is one module's lowered bodies by item key suffix.
type lowered struct {
	w      *world
	b      *built
	bodies map[string]*mir.Body
	keys   []string
	// vBounds and vReduce count the loops VersionLoops versioned (pcOpt.version).
	vBounds, vReduce int
	// nInline counts the calls InlineLeaves inlined (pcOpt.inline).
	nInline int
}

func (w *world) lowerCfg() *Config {
	return &Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Known: w.cfg.Known, Validate: true, Sources: w.sm,
		Impls: ImplsOf(w.c)}
}

// lowerModule checks and lowers every unit of b; an ICE fails the test.
func lowerModule(t testing.TB, w *world, b *built) *lowered {
	t.Helper()
	out := &lowered{w: w, b: b, bodies: map[string]*mir.Body{}}
	cfg := w.lowerCfg()
	for _, id := range b.m.Units() {
		it := w.items.Get(id)
		var body *check.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil {
			t.Fatalf("check ICE in %s: %v", it.Key, ie)
		}
		if len(body.Diags) > 0 {
			t.Fatalf("%s: check diagnostics:\n%s", it.Key, dump(w, body.Diags))
		}
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { mb = Lower(cfg, FromCheck(cfg, w.c, body)) }); ie != nil {
			t.Fatalf("lower ICE in %s: %v", it.Key, ie)
		}
		k := b.key(id)
		out.bodies[k] = mb
		out.keys = append(out.keys, k)
	}
	return out
}

// lowerSrc builds one module "m" over the fake prelude and lowers it.
func lowerSrc(t *testing.T, src string) *lowered {
	t.Helper()
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	return lowerModule(t, w, b)
}

func (l *lowered) print(key string) string {
	b, ok := l.bodies[key]
	if !ok {
		l.w.t.Fatalf("no body %q (have %v)", key, l.keys)
	}
	p := &mir.Printer{Types: l.w.tab, Items: l.w.items}
	return p.Print(b)
}

func (l *lowered) body(key string) *mir.Body {
	b, ok := l.bodies[key]
	if !ok {
		l.w.t.Fatalf("no body %q (have %v)", key, l.keys)
	}
	return b
}

// wantIn fails unless every fragment occurs in the printed body, in order.
func wantIn(t *testing.T, text string, frags ...string) {
	t.Helper()
	at := 0
	for _, f := range frags {
		i := strings.Index(text[at:], f)
		if i < 0 {
			t.Fatalf("missing %q (in order) in:\n%s", f, text)
		}
		at += i + len(f)
	}
}

func TestEmptyBodies(t *testing.T) {
	l := lowerSrc(t, "F() {\n}\nerror E\nG(): E!void {\n}\nconst C: int = 3\n")
	for _, k := range []string{"F", "G", "C"} {
		if l.body(k) == nil {
			t.Fatalf("no body %s", k)
		}
	}
	g := l.body("G")
	if g.Blocks[len(g.Blocks)-1].Term.Kind != mir.TermReturn && g.Blocks[0].Term.Kind != mir.TermReturn {
		t.Logf("%s", l.print("G"))
	}
	if l.body("C").ParamCount != 0 || l.body("C").Locals[0].Type != types.Int {
		t.Fatalf("const body: %s", l.print("C"))
	}
}

func TestSlice(t *testing.T) {
	l := lowerSrc(t, "Add(a: int, b: int): int {\n\treturn a + b\n}\nF(): int {\n\tx := 1\n\tvar y := Add(x, 2)\n\ty += 3\n\treturn y\n}\n")
	t.Log(l.print("F"))
	wantIn(t, l.print("F"), "Add", "return")
}

func TestShowSample(t *testing.T) {
	l := lowerSrc(t, `error E
error D
Load(id: int): E!int {
	if id < 0 {
		fail E
	}
	return id
}
Log() {
}
Show(id: int): E|D!int {
	defer Log()
	onerr Log()
	v := try Load(id)
	for i in 0..v {
		if i == 3 {
			break
		}
	}
	w := Load(1) else {
		return 0
	}
	return v + w
}
`)
	t.Log(l.print("Show"))
	t.Log(l.print("Load"))
}
