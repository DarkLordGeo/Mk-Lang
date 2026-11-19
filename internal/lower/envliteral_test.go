// Why: a capturing literal inside a contract predicate and a generic fields
// loop with a blank value each need their own MIR shape, or lowering fails.
// What: TestPredicateEnv, TestInvariantLiteralEnv,
// TestFieldsGenericBlankValue.
// How: a requires or ensures predicate builds a capturing literal's
// environment through its &P parameters, and a generic fields loop with a
// blank value keeps its generic form; each test checks the MIR shape.

package lower

import (
	"testing"

	"mk/internal/mir"
)

// TestPredicateEnv: in a requires and an ensures predicate a
// capturing literal reads the function's parameter and result, which the
// predicate holds as &P: each field of the environment is a shared
// reborrow *p of that parameter, so the field's type is &P, and the literal
// reads its capture through its hidden parameter. Before the fix lower ICEd
// "a capture in a body with no environment parameter".
func TestPredicateEnv(t *testing.T) {
	src := "Any(xs: []i32, const keep: fn(&i32): bool): bool {\n\treturn true\n}\n\n" +
		"Has(xs: []i32, limit: i32): i32\n" +
		"\trequires Any(xs, fn(x: &i32): bool { return *x > limit })\n" +
		"\tensures !Any(xs, fn(x: &i32): bool { return *x == result + limit })\n{\n\treturn 0\n}\n"
	l := lowerSrc(t, src)
	for _, tc := range []struct {
		pred, lit string
		fields    int
	}{
		{"Has#requires0", "Has#requires0#fn0", 1},
		{"Has#ensures0", "Has#ensures0#fn0", 2},
	} {
		pb := l.body(tc.pred)
		call := onlyCall(t, l, tc.pred, "m.Any")
		args := pb.Args[call.Args.Start : call.Args.Start+call.Args.Len]
		if len(args) != 3 || !mir.IsEnvType(l.w.tab, pb.OperandType(args[2].Operand)) {
			t.Fatalf("%s: the call of Any passes %d arguments, want (xs, keep, env):\n%s", tc.pred, len(args), l.print(tc.pred))
		}
		// Every shared borrow into the environment record is *p of a
		// parameter of the predicate.
		borrows := 0
		for _, blk := range pb.Blocks {
			for _, st := range blk.Stmts {
				rv := st.Rvalue
				if rv.Kind != mir.RvRef || rv.Borrow != mir.Shared || pb.Locals[rv.Place.Local].Kind != mir.LocalParam {
					continue
				}
				if projKinds(pb, rv.Place) != "deref" {
					t.Errorf("%s: an environment field borrows %s of a parameter, want deref", tc.pred, projKinds(pb, rv.Place))
				}
				if rv.Type != pb.Locals[rv.Place.Local].Type {
					t.Errorf("%s: a field of type %s from a parameter of type %s", tc.pred, l.w.tab.TypeString(rv.Type, nil), l.w.tab.TypeString(pb.Locals[rv.Place.Local].Type, nil))
				}
				borrows++
			}
		}
		if borrows != tc.fields {
			t.Errorf("%s: %d borrows of its parameters, want %d:\n%s", tc.pred, borrows, tc.fields, l.print(tc.pred))
		}
		lb := l.body(tc.lit)
		if int(lb.ParamCount) != 2 || !mir.IsEnvType(l.w.tab, lb.Locals[2].Type) {
			t.Fatalf("%s: ParamCount %d, want the hidden environment after x", tc.lit, lb.ParamCount)
		}
		if n := countProj(lb, 2); n != tc.fields {
			t.Errorf("%s: %d places rooted at the environment, want %d", tc.lit, n, tc.fields)
		}
	}
}

// TestInvariantLiteralEnv: a literal of an invariant reads a
// bare field through its capture of the predicate's self: the place is
// *(*(*env).self).field, never a field of the literal's own first local.
// Before the fix the field was Poison with no diagnostic, a lower ICE.
func TestInvariantLiteralEnv(t *testing.T) {
	l := lowerSrc(t, `Ap(const f: fn(x: int): bool): bool {
	return f(1)
}

struct Span {
	lo: int
	hi: int
	invariant Ap(fn(x: int): bool { return lo <= hi + x })
}
`)
	lb := l.body("Span#invariant0#fn0")
	if int(lb.ParamCount) != 2 || !mir.IsEnvType(l.w.tab, lb.Locals[2].Type) {
		t.Fatalf("the literal's ParamCount %d, want the hidden environment after x: %s", lb.ParamCount, l.print("Span#invariant0#fn0"))
	}
	ps := envPlaces(lb, 2)
	if len(ps) != 2 {
		t.Fatalf("%d places rooted at the environment, want 2 (lo, hi): %s", len(ps), l.print("Span#invariant0#fn0"))
	}
	for _, p := range ps {
		if ks := projKinds(lb, p); ks != "deref.field0.deref.deref.field0" && ks != "deref.field0.deref.deref.field1" {
			t.Errorf("a field read steps %s, want deref.field0.deref.deref.fieldN", ks)
		}
	}
	call := onlyCall(t, l, "Span#invariant0", "m.Ap")
	pb := l.body("Span#invariant0")
	if args := pb.Args[call.Args.Start : call.Args.Start+call.Args.Len]; len(args) != 2 || !mir.IsEnvType(l.w.tab, pb.OperandType(args[1].Operand)) {
		t.Fatalf("the invariant's call of Ap passes %d arguments, want (f, env)", len(args))
	}
}

// TestFieldsGenericBlankValue: a generic fields loop whose value is blank
// (for k, _ and for _, _) lowers to the generic form, one loop over the
// hidden generic, as one with a named value does. Before the fix lower
// unrolled check's zero copies, the loop vanished while the body kept the
// hidden generic in GenericCount, and mono ICEd "1 generic arguments for 2
// generics" on every call.
func TestFieldsGenericBlankValue(t *testing.T) {
	for _, tc := range []struct{ head, use string }{
		{"_, v", "v"},
		{"k, _", "k"},
		{"_, _", "n"},
	} {
		src := "Count[A: Fields[Format] + Plain](args: A): i32 {\n\tvar n: i32 = 0\n\tfor " + tc.head + " in fields(&args) {\n\t\tn += 1\n\t\t_ = " + tc.use + "\n\t}\n\treturn n\n}\n"
		l := lowerSrc(t, src)
		b := l.body("Count")
		loops := 0
		for _, lp := range b.Loops {
			if lp.FieldsOf != 0 {
				loops++
				if lp.FieldsParam != 1 {
					t.Errorf("%s: the loop's hidden generic is %d, want 1", tc.head, lp.FieldsParam)
				}
			}
		}
		if b.GenericCount != 2 || loops != 1 {
			t.Errorf("%s: GenericCount %d and %d fields loops, want 2 and 1:\n%s", tc.head, b.GenericCount, loops, l.print("Count"))
		}
	}
}
