// Why: follow-up tests for else on a non-call E!T value (unionelse_test.go).
// What: TestElseExitKinds, TestElseErrorSet,
// TestElsePlaces, TestDroppedLinearPayload.
// How: an else on a non-call E!T value exits as an else handler, reaches
// its handler for every member of an error set, lowers on every place shape
// and in a generic body, and leaves a dropped linear payload in a T
// temporary.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

// TestElseExitKinds: the handler is an else handler of an error
// (scopeElseHandler): its return is ExitElse, and a break out of it runs
// onerr once with an else-exit goto, as TestBreakFromBlockInsideHandler
// pins for a call. On a parameter and on a local.
func TestElseExitKinds(t *testing.T) {
	l := lowerSrc(t, unionSrc+`Log() {
}
Param(xs: []int, r: Bad!int, c: bool): int {
	var s := 0
	for x in xs {
		v := r else {
			if c {
				onerr Log()
				break
			}
			return 0
		}
		s += v + x
	}
	return s
}
Local(xs: []int, c: bool): int {
	var s := 0
	for x in xs {
		var r: Bad!int = Get(x)
		v := r else {
			if c {
				onerr Log()
				break
			}
			return 0
		}
		s += v
	}
	return s
}
`)
	for _, k := range []string{"Param", "Local"} {
		f := wantUnionElse(t, l, k, " as ok).0")
		if count(f, "call m.Log()") != 1 {
			t.Errorf("%s: one Log copy, on the break:\n%s", k, f)
		}
		elseExits, elseRets, normalRets := 0, 0, 0
		for _, blk := range l.body(k).Blocks {
			switch {
			case blk.Term.Kind == mir.TermGoto && blk.Term.ElseExit:
				elseExits++
			case blk.Term.Kind == mir.TermReturn && blk.Term.Exit == mir.ExitElse:
				elseRets++
			case blk.Term.Kind == mir.TermReturn && blk.Term.Exit == mir.ExitNormal:
				normalRets++
			}
		}
		if elseExits != 1 || elseRets != 1 || normalRets != 1 {
			t.Errorf("%s: want one else-exit goto, one return else and one return normal, got %d, %d, %d:\n%s", k, elseExits, elseRets, normalRets, f)
		}
		wantIn(t, f, "return else")
		wantIn(t, f, "else-exit")
	}
}

const unionSetSrc = `error NotFound(id: int)
error Denied

type LoadError = NotFound | Denied

L(k: int): LoadError!int {
	if k < 0 {
		fail Denied
	}
	if k == 0 {
		fail NotFound(id: 42)
	}
	return k
}

U(k: int): int {
	var r: LoadError!int = L(k)
	return r else e {
		match e {
			NotFound(id) {
				id
			}
			Denied {
				7
			}
		}
	}
}
`

// TestElseErrorSet: an error set of two members; each non-zero
// discriminant reaches the handler and e holds the member that failed.
func TestElseErrorSet(t *testing.T) {
	l := lowerSrc(t, unionSetSrc)
	s := wantUnionElse(t, l, "U", " as ok).0", " as err)")
	found := false
	for _, lc := range l.body("U").Locals {
		found = found || lc.Kind == mir.LocalUser && l.w.in.Str(lc.Name) == "e" && strings.Contains(l.w.tab.TypeString(lc.Type, nil), "|")
	}
	if !found {
		t.Errorf("e is no user local of the error set:\n%s", s)
	}
	got, err := evalH(t, unionSetSrc+`H(): int {
	return U(5) * 10000 + U(0) * 100 + U(-1)
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != 5*10000+42*100+7 {
		t.Errorf("H() = %d, want %d", got, 5*10000+42*100+7)
	}
}

// TestElsePlaces: the operand as a deref, as a field (of a struct and
// through a reference), an else as the value of another's handler, and the
// generic bodies (Bad!T, E!int with an error-set parameter).
func TestElsePlaces(t *testing.T) {
	l := lowerSrc(t, unionSrc+`error Other(n: int)

struct Pair {
	r: Bad!int
	k: int
}

Deref(p: &(Bad!int)): int {
	return *p else { 4 }
}

Field(p: Pair): int {
	v := p.r else e {
		_ = e
		5
	}
	return v + p.k
}

FieldRef(p: &Pair): int {
	v := p.r else {
		6
	}
	return v + p.k
}

Nested(r: Bad!int, q: Other!int): int {
	return r else {
		q else e {
			_ = e
			3
		}
	}
}

Or[T](r: Bad!T): ?T {
	v := r else {
		return none
	}
	return some(v)
}

Eh[E](r: E!int): int {
	return r else e {
		_ = e
		11
	}
}
`)
	for k, frags := range map[string][]string{
		"Deref":    {"copy (*_1)", "Discriminant(", " as ok).0", "= int 4"},
		"Field":    {"= copy _1.0\n", "Discriminant(", " as ok).0", " as err)", "= int 5"},
		"FieldRef": {"= copy (*_1).0\n", "Discriminant(", " as ok).0", "= int 6"},
	} {
		wantUnionElse(t, l, k)
		wantIn(t, l.print(k), frags...)
	}
	// The inner else's value is the outer handler's: both write the outer
	// destination (_3), and each operand is switched on once.
	n := wantUnionElse(t, l, "Nested")
	if count(n, "Discriminant(") != 2 || count(n, " as ok).0") != 2 {
		t.Errorf("Nested: two switches, two ok payloads:\n%s", n)
	}
	wantIn(t, n, "= copy _1\n", "Discriminant(", "= copy _2\n", "Discriminant(", "= int 3")
	for _, k := range []string{"Or", "Eh"} {
		s := wantUnionElse(t, l, k, " as ok).0")
		if count(s, "move _1\n")+count(s, "copy _1\n") != 1 {
			t.Errorf("%s: r is not taken exactly once:\n%s", k, s)
		}
	}
	wantIn(t, l.print("Eh"), ": $0 // e\n", " as err)", "= int 11")
	got, err := evalH(t, unionSrc+`error Other(n: int)

GetO(n: int): Other!int {
	if n < 0 {
		fail Other(n: n)
	}
	return n
}

N(a: int, b: int): int {
	var r: Bad!int = Get(a)
	var q: Other!int = GetO(b)
	return r else e {
		_ = e
		q else f {
			match f {
				Other(n: k) {
					0 - k
				}
			}
		}
	}
}

H(): int {
	return N(1, 2) * 10000 + N(-1, 3) * 100 + N(-1, -4)
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != 10304 {
		t.Errorf("H() = %d, want 10304", got)
	}
}

// TestDroppedLinearPayload: in the statement form a linear payload
// moves out of the E!T temporary into a T temporary on the success edge, as
// a call's success edge leaves it in the call's destination, so an
// unconsumed payload is a Res for borrow (E0402 with Res's release facts),
// not a E!Res. The value form keeps assigning its destination directly.
func TestDroppedLinearPayload(t *testing.T) {
	l := lowerSrc(t, resSrc+`Drop(r: E!Res) {
	_ = r else { }
}

Stmt(r: E!Res): int {
	v := r else {
		return 1
	}
	v.Abandon()
	return 0
}

Value(r: E!Res): int {
	v := r else {
		Res(h: 2)
	}
	v.Abandon()
	return 0
}

Bare(r: E!int) {
	_ = r else { }
}
`)
	resTemp := func(k string) mir.LocalID {
		for i, lc := range l.body(k).Locals {
			if lc.Kind == mir.LocalTemp && l.w.tab.TypeString(lc.Type, nil) == "Res" {
				return mir.LocalID(i)
			}
		}
		return mir.NoLocal
	}
	for _, k := range []string{"Drop", "Stmt"} {
		s := wantUnionElse(t, l, k, " = move (_2 as ok).0\n")
		if resTemp(k) == mir.NoLocal || !strings.Contains(s, "_"+itoa(int(resTemp(k)))+" = move (_2 as ok).0\n") {
			t.Errorf("%s: the ok payload is not moved into a Res temporary:\n%s", k, s)
		}
	}
	// Value: the destination _2 (a temp of Res) is assigned the payload.
	wantUnionElse(t, l, "Value", "_2 = move (_3 as ok).0\n")
	if p := l.print("Bare"); strings.Contains(p, " as ok).0") {
		t.Errorf("Bare: a plain payload is dropped with no move:\n%s", p)
	}
	// The return form runs through the Res temporary: ok gives the payload
	// moved through it, err returns from the handler.
	src := `error Bad(code: int)
linear struct Res {
	h: int
}
Res.Done(self): int {
	Res(h) := self
	return h
}
Open(n: int): Bad!Res {
	if n < 0 {
		fail Bad(code: n)
	}
	return Res(h: n)
}
F(r: Bad!Res): int {
	v := r else {
		return 200
	}
	return v.Done()
}
H(): int {
	return F(Open(4)) * 1000 + F(Open(-3))
}
`
	lf := lowerSrc(t, src)
	f := wantUnionElse(t, lf, "F", " = move (_2 as ok).0\n")
	found := false
	for _, lc := range lf.body("F").Locals {
		found = found || lc.Kind == mir.LocalTemp && lf.w.tab.TypeString(lc.Type, nil) == "Res"
	}
	if !found {
		t.Errorf("F: no Res temporary on the success edge:\n%s", f)
	}
	got, err := evalH(t, src)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != 4*1000+200 {
		t.Errorf("H() = %d, want %d", got, 4*1000+200)
	}
}
