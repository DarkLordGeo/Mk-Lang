// Why: an ensures on a void result and error-union values must lower to MIR
// that validates, and error returns must carry the trace calls into std.rt.
// What: TestEnsuresVoidResult, TestTraceCalls,
// TestErrorUnionValues.
// How: lowers each shape with Validate on and checks the calls and values
// in the MIR.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestEnsuresVoidResult is the FuzzCheck crasher `A() ensures 0 {}`
// (seeds lower_ensures_int and e463cef808bbda91).
// Every ensures predicate takes result: &R (check's declContract), also
// when R is void, and lower passed no argument for a void success value, so
// Validate's M5 refused the call ("0 arguments, callee has 1 parameters").
// The valid `A() ensures true {}` failed the same way.
func TestEnsuresVoidResult(t *testing.T) {
	l := lowerSrc(t, `error Bad

A() ensures true {
}

B(x: int) ensures x > 0 {
}

C(ok: bool): Bad!void ensures true {
	if !ok {
		fail Bad
	}
}

D(x: int): int ensures result > x {
	return x + 1
}
`)
	wantIn(t, l.print("A"), "#ensures0", "panic contract")
	wantIn(t, l.print("B"), "#ensures0", "panic contract")
	wantIn(t, l.print("C"), "#ensures0", "panic contract")
	wantIn(t, l.print("D"), "#ensures0", "panic contract")
	if n := count(l.print("A"), "#ensures0"); n != 1 {
		t.Fatalf("A: one ensures call, got %d:\n%s", n, l.print("A"))
	}
}

// rtStub is a std.rt with the runtime's trace hooks and Site.
const rtStub = "export struct Site {\n\texport file: str\n\texport line: u32\n\texport col: u32\n}\n\nexport TraceFail(site: &Site) {\n}\n\nexport TraceTry(site: &Site) {\n}\n\nexport error RtBad\n\nexport RtF(): RtBad!void {\n\tfail RtBad\n}\n"

// TestTraceCalls is the dev error-return trace: before each error
// return, TraceFail(&site) for a fail of a new error value and
// TraceTry(&site) for a try edge or a fail of a bound error, the site the
// return's span. std.rt's own bodies do not trace, and without std.rt
// nothing is emitted (every other lower test).
func TestTraceCalls(t *testing.T) {
	w := newWorld(t, true)
	rt := w.add("std.rt", opt{}, rtStub)
	rt.wantClean(t)
	if s := lowerModule(t, w, rt).print("RtF"); strings.Contains(s, "Trace") {
		t.Fatalf("std.rt bodies do not trace:\n%s", s)
	}
	b := w.add("m", opt{}, `error Bad

F(ok: bool): Bad!int {
	if !ok {
		fail Bad
	}
	return 1
}

G(): Bad!int {
	x := try F(true)
	return x
}

H(e: Bad): Bad!void {
	fail e
}

K(): int {
	return 2
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	f := l.print("F")
	wantIn(t, f, "call std.rt.TraceFail(const", "return fail")
	if count(f, "std.rt.Trace") != 1 {
		t.Fatalf("F: one trace call:\n%s", f)
	}
	g := l.print("G")
	wantIn(t, g, "call std.rt.TraceTry(const", "return try")
	if count(g, "TraceFail") != 0 {
		t.Fatalf("G: no TraceFail:\n%s", g)
	}
	wantIn(t, l.print("H"), "call std.rt.TraceTry(const", "return fail")
	if strings.Contains(l.print("K"), "std.rt.Trace") {
		t.Fatalf("K has no error return:\n%s", l.print("K"))
	}
	// The site: F's `fail Bad` at 5:3 of m's file.
	body := l.body("F")
	found := false
	for _, bk := range body.Blocks {
		tm := bk.Term
		if tm.Kind != mir.TermCall || tm.Callee.Kind != mir.CallDirect {
			continue
		}
		if w.items.Get(tm.Callee.Fn.Item).Key.String() != "std.rt.TraceFail" {
			continue
		}
		found = true
		arg := body.Args[tm.Args.Start]
		ref := w.consts.Get(arg.Operand.Const)
		site := w.consts.Get(ref.Relocs[0].Data)
		if file := w.consts.Get(site.Relocs[0].Data); string(file.Bytes) != w.sm.File(body.Spans[0].File).Path || types.LoadUint(site.Bytes[8:], 8) != uint64(len(file.Bytes)) {
			t.Errorf("site file %q, want %q", file.Bytes, w.sm.File(body.Spans[0].File).Path)
		}
		if line, col := types.LoadUint(site.Bytes[16:], 4), types.LoadUint(site.Bytes[20:], 4); line != 5 || col != 3 {
			t.Errorf("site %d:%d, want 5:3", line, col)
		}
	}
	if !found {
		t.Fatal("no TraceFail call in F")
	}
}

// TestErrorUnionValues covers two E0000s the corpus found: check
// accepts an E!T call under no try, else, match or Must when its value is
// held (`var r: E!T = F()`), and a match with ok and err arms on an E!T
// place. lower ICEd on both ("a fallible call not under try ..., E0802
// (check gap)" and "pattern *ast.OkPattern in a match"). Now the call is
// materialized (ok(dest) on its target, err(e) on an EdgeMatch block,
// joined into the E!T place) and the match switches on the discriminant to
// the SelOk payload or the SelErr error-set value. Every body validates
// (lowerSrc has Validate on).
func TestErrorUnionValues(t *testing.T) {
	l := lowerSrc(t, `error Bad
error Worse(n: int)

Get(n: int): Bad|Worse!int {
	if n == 1 {
		fail Bad
	}
	return n
}

Void(): Bad!void {
}

Code(n: int): int {
	var r: Bad|Worse!int = Get(n)
	match r {
		ok(v) {
			return v
		}
		err(Bad) {
			return -1
		}
		err(Worse(n: k)) {
			return -k
		}
	}
}

Ref(n: int): int {
	var r: Bad|Worse!int = Get(n)
	match &r {
		ok(v) {
			return *v
		}
		_ {
			return 0
		}
	}
}

Hold() {
	var v: Bad!void = Void()
	match v {
		ok(_) {
		}
		err(_) {
		}
	}
}
`)
	for key, frags := range map[string][]string{
		"Code": {"err match", "err(move", "ok(move", "Discriminant(", " as err))", " as ok).0", " as m.Worse).0"},
		"Ref":  {"err match", "err(move", "ok(move", "Discriminant(", " as ok).0"},
		"Hold": {"err match", "err(move", "ok()", "Discriminant("},
	} {
		s := l.print(key)
		for _, f := range frags {
			wantIn(t, s, f)
		}
	}
}
