// Why: capturing literals and const fn parameters had gaps that let a
// release build crash or print wrong values; each must stay fixed.
// What: TestE0609_ConstNotFn, TestE0705_ConstFnToConstNotFn,
// TestE0705_AliasCaptured, TestContractCaptures,
// TestFieldsLoopOuterLiteral.
// How: a capturing literal only for a const parameter of fn type, the const
// fn rule by the real type, captures in contracts, and the fields-loop flag.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// codeNames are the codes of ds, in order, with E0208 left out.
func codeNames(ds []*diag.Diagnostic) string {
	var out []string
	for _, d := range ds {
		if d.Code != diag.E0208 {
			out = append(out, d.Code.String())
		}
	}
	return strings.Join(out, " ")
}

const constNotFnDecls = "struct Box2[F] {\n\tf: F\n}\n\n" +
	"Keep[F](const f: F): Box2[F] {\n\treturn Box2[F](f: f)\n}\n\n" +
	"type Pred = fn(&i32): bool\n\n" +
	"AnyP(xs: []i32, const p: Pred): bool {\n\treturn p(&xs[0])\n}\n\n"

// TestE0609_ConstNotFn: a capturing literal passed to a const
// parameter whose declared type is a type parameter is E0609: the callee
// may store or return a const F (a test program printed 777 for 6), and the
// const fn rule binds only a const parameter of fn type. A literal that
// captures nothing stays a value there, and a const parameter whose type is
// an alias of a fn type takes a capturing literal.
func TestE0609_ConstNotFn(t *testing.T) {
	for _, tc := range []struct{ name, src, want, reason string }{
		{"capturing literal to const F", constNotFnDecls + "Mk(k: i32): Box2[fn(i32): i32] {\n\treturn Keep(fn(x: i32): i32 { return x + k })\n}\n", "E0609", "argument"},
		{"parenthesized", constNotFnDecls + "Mk(k: i32): Box2[fn(i32): i32] {\n\treturn Keep((fn(x: i32): i32 { return x + k }))\n}\n", "E0609", "argument"},
		{"no capture to const F", constNotFnDecls + "Mk(): Box2[fn(i32): i32] {\n\treturn Keep(fn(x: i32): i32 { return x + 1 })\n}\n", "", ""},
		{"capturing literal to an alias of a fn type", constNotFnDecls + "F(xs: []i32, limit: i32): bool {\n\treturn AnyP(xs, fn(x: &i32): bool { return *x > limit })\n}\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, ds := capDiags(t, tc.src)
			if got := codeNames(ds); got != tc.want {
				t.Fatalf("codes %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
			if tc.want == "" {
				return
			}
			d := ds[0]
			if fact(d, "reason") != tc.reason || fact(d, "captured") != "[k]" || d.Phase != diag.PhaseTypes ||
				!strings.Contains(d.Message, "whose type is not a `fn` type") {
				t.Fatalf("facts or message:\n%s", dump(w, ds))
			}
		})
	}
}

// TestE0705_ConstFnToConstNotFn: a const fn parameter passed
// on to a const parameter whose type is a type parameter is E0705 use
// argument: the callee takes the value but no environment. Passed on to an
// alias of a fn type it is a forward, accepted.
func TestE0705_ConstFnToConstNotFn(t *testing.T) {
	w, ds := capDiags(t, constNotFnDecls+"Fw(const less: fn(i32): i32): Box2[fn(i32): i32] {\n\treturn Keep(less)\n}\n")
	if codeNames(ds) != "E0705" || fact(ds[0], "use") != "argument" || fact(ds[0], "param") != "less" {
		t.Fatalf("forward into const F:\n%s", dump(w, ds))
	}
	w, ds = capDiags(t, constNotFnDecls+"Fw(xs: []i32, const q: fn(&i32): bool): bool {\n\treturn AnyP(xs, q)\n}\n")
	if len(ds) != 0 {
		t.Fatalf("forward into an alias of a fn type:\n%s", dump(w, ds))
	}
}

// TestE0705_AliasCaptured: a const parameter whose type is an
// alias of a fn type is a const fn parameter by its real type:
// captured by a literal it is E0705 use captured (resolve's flag is
// syntactic and misses the alias), bound it is E0705 bound, called or
// forwarded it is accepted. Before the fix the capture checked clean and
// the release build crashed.
func TestE0705_AliasCaptured(t *testing.T) {
	decls := "type Less = fn(&int, &int): bool\n\n" +
		"Ap(const f: fn(): int): int {\n\treturn f()\n}\n\n" +
		"Fwd(a: &int, const l: Less): bool {\n\treturn l(a, a)\n}\n\n"
	for _, tc := range []struct{ name, body, want, use string }{
		{"captured", "\ta := 1\n\treturn Ap(fn(): int {\n\t\tif less(&a, &a) {\n\t\t\treturn 1\n\t\t}\n\t\treturn 0\n\t})\n", "E0705", "captured"},
		{"bound", "\tg := less\n\t_ = g\n\treturn 0\n", "E0705", "bound"},
		{"called and forwarded", "\ta := 1\n\tif less(&a, &a) && Fwd(&a, less) {\n\t\treturn 1\n\t}\n\treturn 0\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, ds := capDiags(t, decls+"Outer[T: Plain](t: T, const less: Less): int {\n"+tc.body+"}\n")
			if got := codeNames(ds); got != tc.want {
				t.Fatalf("codes %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
			if tc.want != "" && (fact(ds[0], "use") != tc.use || fact(ds[0], "param") != "less") {
				t.Fatalf("facts:\n%s", dump(w, ds))
			}
		})
	}
}

// invariantCapSrc is an invariant whose literal reads two bare fields.
const invariantCapSrc = `Ap(const f: fn(): bool): bool {
	return f()
}

struct Span {
	lo: int
	hi: int
	invariant Ap(fn(): bool { return lo <= hi })
}
`

const contractCapDecls = "Any(xs: []i32, const keep: fn(&i32): bool): bool {\n\tfor x in xs {\n\t\tif keep(&x) {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}\n\n"

// TestContractCaptures: a capturing literal in a requires or
// ensures predicate may read the function's parameters and result, and one
// in an invariant the struct's fields (through self), which
// the predicate holds as &P: the predicate's signature takes each
// one a literal names, the environment's sources are those &P parameters,
// and each capture's type is P. Before the fix the captures were Poison with
// no diagnostic, and lower and the instance-limit walk ICEd. A const fn
// parameter of the function, by its real type, is E0705 captured there.
func TestContractCaptures(t *testing.T) {
	src := contractCapDecls + "Has(xs: []i32, limit: i32): i32\n" +
		"\trequires Any(xs, fn(x: &i32): bool { return *x > limit })\n" +
		"\tensures !Any(xs, fn(x: &i32): bool { return *x == result + limit })\n{\n\treturn 0\n}\n"
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	bodies := map[string]*Body{}
	for _, id := range b.m.Units() {
		body := w.c.Check(b.m, id)
		if len(body.Diags) > 0 {
			t.Fatalf("%s:\n%s", w.items.Get(id).Key, dump(w, body.Diags))
		}
		bodies[w.items.Get(id).Key.String()] = body
	}
	for _, tc := range []struct {
		pred, lit string
		caps      []string // name:type of each capture, byte order
	}{
		{"m.Has#requires0", "m.Has#requires0#fn0", []string{"limit:i32"}},
		{"m.Has#ensures0", "m.Has#ensures0#fn0", []string{"limit:i32", "result:i32"}},
	} {
		pb, lb := bodies[tc.pred], bodies[tc.lit]
		if pb == nil || lb == nil {
			t.Fatalf("no unit %s or %s", tc.pred, tc.lit)
		}
		if len(pb.Envs) != 1 || lb.EnvRef == types.NoType {
			t.Fatalf("%s: %d environments, literal EnvRef %s", tc.pred, len(pb.Envs), w.tab.TypeString(lb.EnvRef, nil))
		}
		var got []string
		for _, c := range lb.Captures {
			got = append(got, w.in.Str(c.Name)+":"+w.tab.TypeString(c.Type, nil))
		}
		if strings.Join(got, " ") != strings.Join(tc.caps, " ") {
			t.Errorf("%s captures %v, want %v", tc.lit, got, tc.caps)
		}
		for i, s := range pb.Envs[0].Sources {
			l := pb.Locals[s.Local]
			if s.Kind != UseLocal || l.Param == 0 || l.Name != lb.Captures[i].Name || l.Type != w.tab.Ref(lb.Captures[i].Type) {
				t.Errorf("%s source %d is %+v (%s), want the predicate's &P parameter", tc.pred, i, s, w.tab.TypeString(l.Type, nil))
			}
		}
	}
	// An invariant's literal reads bare fields through its capture of the
	// predicate's self (&Span).
	w2 := newWorld(t, true)
	b2 := w2.add("m", opt{}, invariantCapSrc)
	b2.wantClean(t)
	for _, id := range b2.m.Units() {
		body := w2.c.Check(b2.m, id)
		if len(body.Diags) > 0 {
			t.Fatalf("%s: %s", w2.items.Get(id).Key, dump(w2, body.Diags))
		}
		if w2.items.Get(id).Key.String() == "m.Span#invariant0#fn0" {
			if len(body.Captures) != 1 || w2.in.Str(body.Captures[0].Name) != "self" || w2.tab.TypeString(body.Captures[0].Type, nil) != "&Span" {
				t.Fatalf("the invariant's literal captures %v, want self: &Span", body.Captures)
			}
		}
	}
	// The function's const fn parameter, also through an alias.
	for _, typ := range []string{"fn(&i32): bool", "Pred"} {
		w, ds := capDiags(t, contractCapDecls+"type Pred = fn(&i32): bool\n\n"+
			"Has(xs: []i32, const p: "+typ+"): i32\n\trequires Any(xs, fn(x: &i32): bool { return p(x) })\n{\n\treturn 0\n}\n")
		var uses []string
		for _, d := range ds {
			if d.Code != diag.E0705 {
				t.Fatalf("%s: %s", typ, dump(w, ds))
			}
			uses = append(uses, fact(d, "use"))
		}
		if !strings.Contains(strings.Join(uses, " "), "captured") {
			t.Errorf("%s: E0705 uses %v, want one captured\n%s", typ, uses, dump(w, ds))
		}
	}
}

// TestFieldsLoopOuterLiteral: a name an outer literal
// declares, inside a fields loop, is no fields-loop capture of an inner
// literal: the outer literal's unit is checked once. The failing program
// had a wrong E0609 fields-loop; a capture of the loop's value stays one.
func TestFieldsLoopOuterLiteral(t *testing.T) {
	decls := "Ap(const f: fn(): i32): i32 {\n\treturn f()\n}\n\n"
	w, ds := capDiags(t, decls+"Count[A: Fields[Format] + Plain](args: A): i32 {\n\tvar n: i32 = 0\n\tfor _, _ in fields(&args) {\n"+
		"\t\tn += Ap(fn(): i32 {\n\t\t\tz: i32 = 3\n\t\t\treturn Ap(fn(): i32 { return z })\n\t\t})\n\t}\n\treturn n\n}\n")
	if len(ds) != 0 {
		t.Fatalf("outer literal's local:\n%s", dump(w, ds))
	}
	w, ds = capDiags(t, decls+"Count[A: Fields[Format] + Plain](args: A): i32 {\n\tvar n: i32 = 0\n\tfor _, v in fields(&args) {\n"+
		"\t\tn += Ap(fn(): i32 {\n\t\t\treturn Ap(fn(): i32 {\n\t\t\t\t_ = v\n\t\t\t\treturn 1\n\t\t\t})\n\t\t})\n\t}\n\treturn n\n}\n")
	// The outer literal's unit and the inner literal's both see the flag at
	// the one use; diag.Order keeps one of the equal diagnostics.
	for _, d := range ds {
		if d.Code != diag.E0609 || fact(d, "reason") != "fields-loop" || d.At != ds[0].At || d.Message != ds[0].Message {
			t.Fatalf("the loop's value through an outer literal:\n%s", dump(w, ds))
		}
	}
	if len(ds) == 0 {
		t.Fatal("the loop's value through an outer literal: no E0609")
	}
}
