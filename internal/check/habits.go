// Why: programmers from other languages write iterator chains and `.await`;
// only the type checker sees these member-shaped habits, and a plain "no
// member" error would not say what Mk wants instead.
//
// What: iteratorHabits, E1202's member set, and valueMiss.
//
// How: a value member that fails exact and folded lookup and is spelled like
// an iterator adapter is E1202; a postfix `.await` is E1204. Both fire only
// after lookup failed, so no program that compiles changes.

package check

import (
	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/types"
)

// iteratorHabits is E1202's member set: `.map`, `.filter`, `.collect` and
// the other common Rust spellings. Each fires
// only after lookup failed, so no program that compiles changes.
var iteratorHabits = map[string]bool{
	"map": true, "filter": true, "collect": true,
	"iter": true, "into_iter": true, "iter_mut": true, "for_each": true, "fold": true,
	"enumerate": true, "zip": true, "rev": true, "sum": true, "filter_map": true, "flat_map": true,
}

// valueMiss reports a failed lookup of fe's member on a value of type t: a
// habit (E1202, E1204) when the name is one, else memberMiss (E0702, E0203).
// call says fe is a call's callee. E1202 is a member call only: a
// field read `t.sum` is more likely a misspelled field, and E0203 lists the
// members. Deleting `.await` from `x.await()` would leave `x()`, so only
// the field form gets E1204's proven fix.
// The habit diagnostics carry facts.habits as the parser's and resolve's do;
// facts.target needs the source line, which check does not hold.
func (u *unit) valueMiss(t types.TypeID, fe *ast.FieldExpr, call bool) {
	name := fe.Name
	nm := u.c.cfg.Interner.Str(name.Name)
	switch {
	case call && iteratorHabits[nm]:
		u.err(diag.E1202, name.Span(), "Mk has no iterator chains; `"+u.str(t)+"` has no member `"+nm+"`").
			Text("type", u.str(t)).Fact("habits", diag.TextList(diag.E1202.String())).
			Alt("write a `for` loop over the value, e.g. `for x in xs { .. }`")
	case nm == "await":
		d := u.err(diag.E1204, name.Span(), "Mk has no `await`; call the function, passing `Io`").In(diag.PhaseTypes).
			Fact("habits", diag.TextList(diag.E1204.String()))
		if x := fe.X; !call && x != nil && x.Span().File == name.Span().File && x.Span().End <= name.Span().Start {
			cut := name.Span()
			cut.Start = x.Span().End
			d.Fix(diag.Fix{Class: diag.Proven, Edits: []diag.Edit{{Span: cut, Text: ""}}, Why: "a call already waits for its result"})
		}
	default:
		u.memberMiss(t, name)
	}
}
