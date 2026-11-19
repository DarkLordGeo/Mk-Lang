// Why: a fn literal passed as a const argument may read the enclosing
// function's locals, so its body and every call to it carry an environment.
// What: TestLiteralBodyEnv, TestCallEnv, TestNoCaptureUnchanged
// and the shape helpers (hasEnvShape, envPlaces, projKinds).
// How: a capturing literal's body takes its environment as a hidden
// trailing parameter and reads each capture through it; the call builds the
// environment after every other operand and passes it as one trailing
// argument per capturing literal, in const-parameter order.

package lower

import (
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

const capLowerDecls = "Sort(xs: mut []i32, const less: fn(&i32, &i32): bool) {\n\tif xs.Len() > 1 && less(&xs[0], &xs[1]) {\n\t\txs[0] = 0\n\t}\n}\n\n" +
	"Two(xs: mut []i32, const a: fn(&i32): bool, n: int, const b: fn(&i32): bool) {\n}\n\n" +
	"struct Box {\n\tn: int\n}\n\n"

// TestLiteralBodyEnv: the literal's body has ParamCount one past its
// signature, the hidden local is a LocalParam of type &(origin: &i32), and a
// capture reads *((*env).origin); Validate passes.
func TestLiteralBodyEnv(t *testing.T) {
	src := capLowerDecls + "ByDistance(xs: mut []i32, origin: i32) {\n\tSort(xs, fn(a: &i32, b: &i32): bool {\n\t\tda := *a - origin\n\t\tdb := *b - origin\n\t\treturn da * da < db * db\n\t})\n}\n"
	l := lowerSrc(t, src)
	b := l.body("ByDistance#fn0")
	if b.ParamCount != 3 {
		t.Fatalf("literal ParamCount %d, want 3", b.ParamCount)
	}
	env := b.Locals[3]
	if env.Kind != mir.LocalParam || env.Param != 3 {
		t.Fatalf("local 3 is %+v, want the hidden LocalParam 3", env)
	}
	if !mir.IsEnvType(l.w.tab, env.Type) {
		t.Fatalf("hidden parameter type %s is no environment type", l.w.tab.TypeString(env.Type, nil))
	}
	if got := l.w.tab.TypeString(env.Type, nil); got != "&(origin: &i32)" {
		t.Errorf("hidden parameter type %s, want &(origin: &i32)", got)
	}
	// Two reads of origin, each through the environment.
	if n := countProj(b, 3); n != 2 {
		t.Errorf("%d places rooted at the environment, want 2:\n%s", n, l.print("ByDistance#fn0"))
	}
	for _, p := range envPlaces(b, 3) {
		ks := projKinds(b, p)
		if ks != "deref.field0.deref" {
			t.Errorf("capture place steps %s, want deref.field0.deref", ks)
		}
	}
	// A literal that captures nothing has no hidden parameter.
	l2 := lowerSrc(t, capLowerDecls+"NoCap(xs: mut []i32) {\n\tSort(xs, fn(a: &i32, b: &i32): bool { return *a < *b })\n}\n")
	if b := l2.body("NoCap#fn0"); b.ParamCount != 2 {
		t.Errorf("a non-capturing literal has ParamCount %d, want 2", b.ParamCount)
	}
}

// TestCallEnv: pass 3 builds &T_i of each source in field order (byte
// order of names), the record and its reference, after the written borrow
// forms, and appends one trailing ArgCopy per capturing literal in
// const-parameter order; a nested literal reborrows its own capture.
func TestCallEnv(t *testing.T) {
	src := capLowerDecls +
		"One(xs: mut []i32, zeta: i32, b: &Box) {\n\talpha := 3\n\tSort(xs, fn(x: &i32, y: &i32): bool { return *x < zeta && b.n > 0 && alpha > 0 })\n}\n\n" +
		"Both(xs: mut []i32, lo: i32, hi: i32) {\n\tTwo(xs, fn(x: &i32): bool { return *x < hi }, 1, fn(x: &i32): bool { return *x > lo })\n}\n\n" +
		"Nested(xs: mut []i32, origin: i32) {\n\tSort(xs, fn(a: &i32, b: &i32): bool {\n\t\tvar ys: [2]i32 = [1, 2]\n\t\tSort(mut ys[..], fn(c: &i32, d: &i32): bool { return *c < origin })\n\t\treturn *a < origin\n\t})\n}\n"
	l := lowerSrc(t, src)

	one := l.body("One")
	call := onlyCall(t, l, "One", "m.Sort")
	args := one.Args[call.Args.Start : call.Args.Start+call.Args.Len]
	if len(args) != 3 {
		t.Fatalf("Sort call has %d arguments, want 3 (xs, less, env):\n%s", len(args), l.print("One"))
	}
	envArg := args[2]
	if envArg.Mode != mir.ArgCopy || !mir.IsEnvType(l.w.tab, one.OperandType(envArg.Operand)) {
		t.Fatalf("trailing argument %v of %s, want a copy of an environment", envArg.Mode, l.w.tab.TypeString(one.OperandType(envArg.Operand), nil))
	}
	if got := l.w.tab.TypeString(one.OperandType(envArg.Operand), nil); got != "&(alpha: &int, b: &&Box, zeta: &i32)" {
		t.Errorf("environment type %s, want fields in byte order of names", got)
	}
	// The record's operands borrow alpha (_4), b (_3), zeta (_2) in that
	// order, after the reborrow of xs, right before the call.
	text := l.print("One")
	wantIn(t, text, "_5 = &_4", "_6 = &_3", "_7 = &_2", "_8 = (alpha: &int, b: &&Box, zeta: &i32)(copy _5, copy _6, copy _7)", "_9 = &_8", "call m.Sort(reborrow copy _1, const", "copy _9) ->")

	// Two capturing literals: two trailing arguments, a's environment then b's.
	both := l.body("Both")
	bc := onlyCall(t, l, "Both", "m.Two")
	bargs := both.Args[bc.Args.Start : bc.Args.Start+bc.Args.Len]
	if len(bargs) != 6 {
		t.Fatalf("Two call has %d arguments, want 6:\n%s", len(bargs), l.print("Both"))
	}
	for i, want := range []string{"&(hi: &i32)", "&(lo: &i32)"} {
		if got := l.w.tab.TypeString(both.OperandType(bargs[4+i].Operand), nil); got != want {
			t.Errorf("environment argument %d is %s, want %s", i, got, want)
		}
	}

	// The nested literal's environment borrows the outer literal's capture.
	outer := l.body("Nested#fn0")
	if outer.ParamCount != 3 {
		t.Fatalf("outer literal ParamCount %d, want 3", outer.ParamCount)
	}
	inner := l.body("Nested#fn0#fn0")
	if inner.ParamCount != 3 {
		t.Fatalf("inner literal ParamCount %d, want 3", inner.ParamCount)
	}
	oc := onlyCall(t, l, "Nested#fn0", "m.Sort")
	oargs := outer.Args[oc.Args.Start : oc.Args.Start+oc.Args.Len]
	if len(oargs) != 3 {
		t.Fatalf("inner Sort call has %d arguments, want 3:\n%s", len(oargs), l.print("Nested#fn0"))
	}
	// One RvRef of a place rooted at the outer environment: the reborrow.
	found := false
	for _, blk := range outer.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvRef && st.Rvalue.Place.Local == 3 {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the nested environment does not reborrow the outer capture:\n%s", l.print("Nested#fn0"))
	}
}

// TestNoCaptureUnchanged: a body with no capturing literal has
// no hidden parameter and no call argument past its callee's parameters,
// over every lowered unit of the corpus (the golden of the MIR text is
// TestCorpusGolden; this is the structural half).
func TestNoCaptureUnchanged(t *testing.T) {
	src := capLowerDecls + "NoCap2(xs: mut []i32, k: i32) {\n\tSort(xs, fn(a: &i32, b: &i32): bool { return *a < *b })\n\tf := fn(a: &i32): bool { return *a > 0 }\n\t_ = f(&k)\n}\n"
	l := lowerSrc(t, src)
	for _, k := range l.keys {
		b := l.bodies[k]
		if hasEnvShape(l.w.tab, b, l.w.lowerCfg()) {
			t.Errorf("%s has a hidden parameter or an environment argument:\n%s", k, l.print(k))
		}
	}
	// Every unit of every verdict-ok corpus block: the shape appears exactly
	// in the bodies check gave an environment (a capturing literal's own, or
	// one that passes a capturing literal).
	core, pre := corpusPrelude(t), testPrelude(t)
	seen, withEnv := 0, 0
	for _, u := range corpusUnits(t) {
		w, b, ok := corpusWorld(t, core, pre, u)
		if !ok {
			continue
		}
		cfg := w.lowerCfg()
		for _, id := range b.m.Units() {
			it := w.items.Get(id)
			var body *check.Body
			var mb *mir.Body
			if ie := source.Guard(it.Key.String(), it.Span, func() {
				body = w.c.Check(b.m, id)
				if len(body.Diags) == 0 {
					mb = Lower(cfg, FromCheck(cfg, w.c, body))
				}
			}); ie != nil || mb == nil {
				continue
			}
			seen++
			want := body.EnvRef != types.NoType || len(body.Envs) > 0
			if want {
				withEnv++
			}
			if got := hasEnvShape(w.tab, mb, cfg); got != want {
				t.Errorf("%s %s: environment shape %v, check's environments %v", u.key, it.Key, got, want)
			}
		}
	}
	if withEnv == 0 || seen < 100 {
		t.Errorf("corpus walk saw %d bodies, %d with an environment", seen, withEnv)
	}
	t.Logf("%d corpus bodies, %d with an environment", seen, withEnv)
}

// hasEnvShape reports a hidden parameter or a call argument past a direct
// callee's declared parameters.
func hasEnvShape(tab *types.Table, b *mir.Body, cfg *Config) bool {
	for i := 1; i <= int(b.ParamCount); i++ {
		if mir.IsEnvType(tab, b.Locals[i].Type) {
			return true
		}
	}
	for _, blk := range b.Blocks {
		t := blk.Term
		if t.Kind != mir.TermCall || t.Callee.Kind != mir.CallDirect {
			continue
		}
		if sig := cfg.callee(t.Callee.Fn.Item); sig != nil && !sig.Variadic && int(t.Args.Len) > len(sig.Params) {
			return true
		}
	}
	return false
}

// onlyCall is the one direct call of the callee whose printed key is fn in
// body key.
func onlyCall(t *testing.T, l *lowered, key, fn string) mir.Terminator {
	t.Helper()
	b := l.body(key)
	var out []mir.Terminator
	for _, blk := range b.Blocks {
		tm := blk.Term
		if tm.Kind == mir.TermCall && tm.Callee.Kind == mir.CallDirect && l.w.items.Get(tm.Callee.Fn.Item).Key.String() == fn {
			out = append(out, tm)
		}
	}
	if len(out) != 1 {
		t.Fatalf("%s: %d calls of %s, want 1:\n%s", key, len(out), fn, l.print(key))
	}
	return out[0]
}

// envPlaces are the places of b's statements and operands rooted at local env
// with a projection.
func envPlaces(b *mir.Body, env mir.LocalID) []mir.Place {
	var out []mir.Place
	add := func(o mir.Operand) {
		if (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Local == env && o.Place.Proj.Len > 0 {
			out = append(out, o.Place)
		}
	}
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind != mir.StAssign {
				continue
			}
			rv := st.Rvalue
			add(rv.A)
			add(rv.B)
			if rv.Kind == mir.RvRef && rv.Place.Local == env {
				out = append(out, rv.Place)
			}
		}
	}
	return out
}

func countProj(b *mir.Body, env mir.LocalID) int { return len(envPlaces(b, env)) }

// projKinds spells a place's steps: deref, fieldN, index, downcast.
func projKinds(b *mir.Body, p mir.Place) string {
	var parts []string
	for _, s := range b.Proj(p) {
		switch s.Kind {
		case mir.ProjDeref:
			parts = append(parts, "deref")
		case mir.ProjField:
			parts = append(parts, "field"+string(rune('0'+s.A)))
		case mir.ProjIndex:
			parts = append(parts, "index")
		default:
			parts = append(parts, "downcast")
		}
	}
	return strings.Join(parts, ".")
}
