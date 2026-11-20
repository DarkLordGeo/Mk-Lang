// Why: habits from other languages (iterator chains, await) deserve a
// pointed diagnostic, and `into` is a flow at fn value conversions and impl
// methods.
// What: TestE1202_IteratorChain, TestE1204_Await, TestE0608_IntoFlow,
// TestPoisonedArgumentNoE0711, TestE1221_MemberSynonyms.
// How: checks small bodies and compares codes, phases and help text.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

func TestE1202_IteratorChain(t *testing.T) {
	const dbl = "Double(x: &i32): i32 {\n\treturn *x * 2\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"map on a slice", dbl + "All(xs: []i32): int {\n\tys := xs.map(Double)\n\treturn ys.Len()\n}\n", "E1202@6:11"},
		{"filter, X09", "Keep(x: &i32): bool {\n\treturn *x > 0\n}\n\nPositive(xs: []i32): int {\n\tys := xs.filter(Keep)\n\treturn ys.Len()\n}\n", "E1202@6:11"},
		{"one per chain", dbl + "All(xs: []i32): int {\n\tys := xs.map(Double).filter(Double).collect()\n\treturn 0\n}\n", "E1202@6:11"},
		{"iter on an array", "F(a: [3]int) {\n\ta.iter()\n}\n", "E1202@2:4"},
		{"on a struct", "struct P {\n\tx: int\n}\n\nF(p: P) {\n\tp.collect()\n}\n", "E1202@6:4"},
		// E1202 is a member call; a field read keeps E0203 and its
		// member list, the likelier help for `t.sum` meaning a field.
		{"field form is E0203", "F(xs: []int): int {\n\tm := xs.map\n\treturn 0\n}\n", "E0203@2:10"},
		{"a record field read is E0203", "F(r: (a: int, b: int)): int {\n\treturn r.sum\n}\n", "E0203@2:11"},
		{"a call on a record", "F(r: (a: int, b: int)) {\n\tr.sum()\n}\n", "E1202@2:4"},
		// Lookup runs first: a declared member of that spelling is no habit,
		// and a folded match is E0207 (S.Map exists).
		{"folded match is E0207", "struct S {\n\tx: int\n}\n\nS.Map(&self): int {\n\treturn 1\n}\n\nF(s: S): int {\n\treturn s.map()\n}\n", "E0207@10:11"},
		{"other names stay E0203", "F(xs: []int) {\n\txs.mapping()\n}\n", "E0203@2:5"},
		{"a pattern is not a habit", "enum E {\n\tA\n}\n\nF(e: E): int {\n\tmatch e {\n\t\t.map {\n\t\t\treturn 1\n\t\t}\n\t\t.A {\n\t\t\treturn 2\n\t\t}\n\t}\n\treturn 0\n}\n", "E0203@7:4"},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E1202@6:11", dbl+"All(xs: []i32): int {\n\tys := xs.map(Double)\n\treturn ys.Len()\n}\n")
	d := firstCode(t, b.bodyDiags(t), diag.E1202)
	if d.Phase != diag.PhaseTypes {
		t.Errorf("E1202 phase %v, want types", d.Phase)
	}
	if got := factText(w, d, "habits"); got != "[E1202]" {
		t.Errorf("habits %q, want [E1202]", got)
	}
	if got := factText(w, d, "type"); got != "[]i32" {
		t.Errorf("type %q, want []i32", got)
	}
	if len(d.Fixes) != 0 || len(d.Alternatives) == 0 {
		t.Errorf("fixes %v, alternatives %v: want no proven fix (04e: compiles) and a for-loop alternative", d.Fixes, d.Alternatives)
	}
}

func TestE1204_Await(t *testing.T) {
	const src = "Wait(n: int): int {\n\treturn n.await\n}\n"
	runBodyCases(t, []bodyCase{
		{"field form", src, "E1204@2:11"},
		{"call form", "Get(): int {\n\treturn 1\n}\n\nF(): int {\n\treturn Get().await()\n}\n", "E1204@6:15"},
		{"on a struct", "struct P {\n\tx: int\n}\n\nF(p: P): int {\n\treturn p.await\n}\n", "E1204@6:11"},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E1204@2:11", src)
	d := firstCode(t, b.bodyDiags(t), diag.E1204)
	// DefaultPhase(E1204) is parse (the parser's prefix form); check's
	// postfix form is in group G4, or it would hide every G4
	// diagnostic of the build.
	if d.Phase != diag.PhaseTypes {
		t.Errorf("E1204 phase %v, want types", d.Phase)
	}
	if got := factText(w, d, "habits"); got != "[E1204]" {
		t.Errorf("habits %q, want [E1204]", got)
	}
	if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven || len(d.Fixes[0].Edits) != 1 {
		t.Fatalf("fixes %+v, want one proven edit", d.Fixes)
	}
	e := d.Fixes[0].Edits[0]
	start := uint32(strings.Index(src, ".await"))
	if e.Span.Start != start || e.Span.End != start+uint32(len(".await")) || e.Text != "" {
		t.Errorf("edit %+v, want [%d, %d) deleted", e, start, start+uint32(len(".await")))
	}
	// The call form keeps no proven fix: deleting `.await` would leave `x()`.
	b = checkBody(t, newWorld(t, true), "E1204@6:15", "Get(): int {\n\treturn 1\n}\n\nF(): int {\n\treturn Get().await()\n}\n")
	if d := firstCode(t, b.bodyDiags(t), diag.E1204); len(d.Fixes) != 0 {
		t.Errorf("call form fixes %+v, want none", d.Fixes)
	}
}

// TestE0608_IntoFlow: a parameter's `into` is a flow, so a fn value or
// impl method whose `into` the target does not declare is E0608, not E0301
// or E0707, and one with fewer flows than the target fits.
func TestE0608_IntoFlow(t *testing.T) {
	const put = "Put(c: &Cell[&i32], v: &i32 into c) {\n\tc.Store(v)\n}\n\n"
	const peek = "Peek(c: &Cell[&i32], v: &i32) {\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"H21.reject", put + "Apply(f: fn(c: &Cell[&i32], v: &i32), c: &Cell[&i32], v: &i32) {\n\tf(c, v)\n}\n\nBad(c: &Cell[&i32], v: &i32) {\n\tApply(Put, c, v)\n}\n", "E0608@10:8"},
		{"H21.accept", put + "Apply(f: fn(c: &Cell[&i32], v: &i32 into c), c: &Cell[&i32], v: &i32) {\n\tf(c, v)\n}\n\nGood(c: &Cell[&i32], v: &i32) {\n\tApply(Put, c, v)\n}\n", ""},
		{"fewer flows than the target", peek + "Apply(f: fn(c: &Cell[&i32], v: &i32 into c), c: &Cell[&i32], v: &i32) {\n\tf(c, v)\n}\n\nGood(c: &Cell[&i32], v: &i32) {\n\tApply(Peek, c, v)\n}\n", ""},
		{"typed binding", put + "F() {\n\tf: fn(c: &Cell[&i32], v: &i32) = Put\n}\n", "E0608@6:35"},
		{"generic parameter", put + "Apply[T](f: fn(c: &Cell[T], v: T), c: &Cell[T], v: T) {\n}\n\nBad(c: &Cell[&i32], v: &i32) {\n\tApply(Put, c, v)\n}\n", "E0608@9:8"},
		{"other shape stays E0301", put + "Apply(f: fn(c: &Cell[&i32], v: &i64), c: &Cell[&i32]) {\n}\n\nBad(c: &Cell[&i32]) {\n\tApply(Put, c)\n}\n", "E0301@9:8"},
	})
	const keeper = "interface Keeper {\n\tKeep(mut self, x: &i32%s)\n}\n\nstruct Slot {\n\tv: ?&i32\n}\n\nimpl Keeper for Slot {\n\tKeep(mut self, x: &i32%s) {\n\t\tself.v = some(x)\n\t}\n}\n"
	at := func(iface, impl string) string {
		return strings.Replace(strings.Replace(keeper, "%s", iface, 1), "%s", impl, 1)
	}
	runBodyCases(t, []bodyCase{
		{"EV530 reject", at("", " into self"), "E0608@10:2"},
		{"EV530 accept", at(" into self", " into self"), ""},
		{"impl declares fewer", at(" into self", ""), ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0608@10:2", at("", " into self"))
	d := firstCode(t, b.diags(), diag.E0608)
	if got := factText(w, d, "found"); got != "x into self" {
		t.Errorf("found %q, want `x into self`", got)
	}
	if got := factText(w, d, "expected"); got != "none" {
		t.Errorf("expected %q, want none", got)
	}
}

// TestPoisonedArgumentNoE0711: `Use(&s)` with `s` poisoned by
// E1003 meets `&T` with Poison; T has no other source, so no E0711 joins.
func TestPoisonedArgumentNoE0711(t *testing.T) {
	const use = "Use[T](x: &T) {\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"H17", use + "Bad(io: Io) {\n\ts := io.Scope()\n\tUse(&s)\n}\n", "E1003@5:7"},
		{"optional", "Opt[T](x: ?T) {\n}\n\nBad(io: Io) {\n\ts := io.Scope()\n\tOpt(some(s))\n}\n", "E1003@5:7"},
		{"nested record", "Rec[T](x: &(a: T, b: int)) {\n}\n\nBad(io: Io) {\n\ts := io.Scope()\n\tRec(&(a: s, b: 1))\n}\n", "E1003@5:7"},
		{"unknown name", use + "Bad() {\n\tUse(&nope)\n}\n", "E0201@5:7"},
		{"still E0711 with no argument", "Mk[T](): int {\n\treturn 0\n}\n\nF(): int {\n\treturn Mk()\n}\n", "E0711@6:9"},
	})
}

// TestE1221_MemberSynonyms: a member spelled as Rust spells it is E1221
// naming the Mk member (`.unwrap` on `?T` renamed to `.Must` by a proven
// fix) or the Mk call; on a type without `Must`, `.unwrap` stays E0203.
func TestE1221_MemberSynonyms(t *testing.T) {
	const unwrap = "F(x: ?int): int {\n\treturn x.unwrap()\n}\n"
	runBodyCases(t, []bodyCase{
		{"unwrap", unwrap, "E1221@2:11"},
		{"expect", "F(x: ?int): int {\n\treturn x.expect(\"no\")\n}\n", "E1221@2:11"},
		{"clone", "F(x: int): int {\n\treturn x.clone()\n}\n", "E1221@2:11"},
		{"to_string", "F(x: int) {\n\tx.to_string()\n}\n", "E1221@2:4"},
		{"unwrap on an int", "F(x: int): int {\n\treturn x.unwrap()\n}\n", "E0203@2:11"},
		{"unwrap on an error union", "error Bad\n\nG(): Bad!int {\n\treturn 1\n}\n\nF(): int {\n\treturn G().unwrap()\n}\n", "E1221@8:13"},
		{"other member of an error union stays E0802", "error Bad\n\nG(): Bad!int {\n\treturn 1\n}\n\nF(): int {\n\treturn G().Len()\n}\n", "E0802@8:9"},
	})
	w := newWorld(t, true)
	d := firstCode(t, checkBody(t, w, "E1221@2:11", unwrap).bodyDiags(t), diag.E1221)
	if !strings.Contains(d.Message, "Mk spells it `Must`") || factText(w, d, "habits") != "[E1221]" {
		t.Errorf("message %q habits %q", d.Message, factText(w, d, "habits"))
	}
	if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven || d.Fixes[0].Edits[0].Text != "Must" {
		t.Errorf("fixes %+v, want one proven rename to Must", d.Fixes)
	}
	d = firstCode(t, checkBody(t, newWorld(t, true), "E1221@2:11", "F(x: int): int {\n\treturn x.clone()\n}\n").bodyDiags(t), diag.E1221)
	if len(d.Fixes) != 0 || strings.Join(d.Alternatives, "|") != "try arena.Dup(s)" {
		t.Errorf("clone: fixes %+v alternatives %q", d.Fixes, d.Alternatives)
	}
}

func firstCode(t *testing.T, ds []*diag.Diagnostic, c diag.Code) *diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == c {
			return d
		}
	}
	t.Fatalf("no %v among %d diagnostics", c, len(ds))
	return nil
}

// factText is d's fact key as factString renders it, "" when absent.
func factText(w *world, d *diag.Diagnostic, key string) string {
	_ = w
	for _, f := range d.Facts {
		if f.Key == key {
			return factString(f.Value)
		}
	}
	return ""
}
