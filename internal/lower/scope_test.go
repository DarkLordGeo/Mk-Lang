// Why: a try scope's E!void is the err field of its Scope[E] local, read
// after the join on the fall-through exit only; every exit lowers
// differently.
// What: TestTryScopeFinish and lowerNoValidate.
// How: lowers one try scope with every kind of exit and checks each edge's
// defers, joins and err reads, then validates the body by hand.

package lower

import (
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
)

// lowerNoValidate is lowerModule with Validate off, for tests that validate
// by hand (or mutate a body first); it returns the env to validate with.
func lowerNoValidate(t *testing.T, src string) (*lowered, func(key string) error) {
	t.Helper()
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	out := &lowered{w: w, b: b, bodies: map[string]*mir.Body{}}
	cfg := w.lowerCfg()
	cfg.Validate = false
	sigs := map[string]*mir.Env{}
	for _, id := range b.m.Units() {
		it := w.items.Get(id)
		var body *check.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil {
			t.Fatalf("check ICE in %s: %v", it.Key, ie)
		}
		if len(body.Diags) > 0 {
			t.Fatalf("%s: check diagnostics:\n%s", it.Key, dump(w, body.Diags))
		}
		in := FromCheck(cfg, w.c, body)
		var mb *mir.Body
		if ie := source.Guard(it.Key.String(), it.Span, func() { mb = Lower(cfg, in) }); ie != nil {
			t.Fatalf("lower ICE in %s: %v", it.Key, ie)
		}
		k := b.key(id)
		out.bodies[k] = mb
		out.keys = append(out.keys, k)
		sigs[k] = &mir.Env{Types: cfg.Types, Sig: in.Sig(), Callee: cfg.callee, Known: cfg.Known, Facts: in.Facts}
	}
	return out, func(key string) error { return out.body(key).Validate(sigs[key]) }
}

const tryScopeSrc = `error E
error D
Work(): E!void {
}
Log() {
}
Run(io: Io, n: int): E|D!void {
	onerr Log()
	try scope s := io.ScopeOf[E]() {
		defer Log()
		onerr Log()
		s.go Work()
		if n > 1 {
			return
		}
		if n < 0 {
			fail D
		}
	}
	Log()
}
`

// TestTryScopeFinish: on the fall-through, the block's defer (not its
// onerr), one join without cancel, then the err field read and tested
// before the scope ends; the error edge widens, runs only the enclosing
// onerr, and returns ExitTry; the early return and fail join with cancel
// and never read err.
func TestTryScopeFinish(t *testing.T) {
	l, validate := lowerNoValidate(t, tryScopeSrc)
	f := l.print("Run")
	t.Log(f)
	if n := count(f, "join _"); n != 3 {
		t.Fatalf("want 3 joins (return, fail, fall-through), got %d:\n%s", n, f)
	}
	if n := count(f, " cancel"); n != 2 {
		t.Fatalf("want 2 cancelling joins (return, fail), got %d:\n%s", n, f)
	}
	if n := strings.Count(f, ".1"); n < 1 {
		t.Fatalf("no read of the err field (field 1 of Scope[E]):\n%s", f)
	}
	// Exactly one err read: one discriminant of the Scope local's field 1.
	b := l.body("Run")
	sv := mir.NoLocal
	for i, lc := range b.Locals {
		if lc.Kind == mir.LocalScope {
			sv = mir.LocalID(i)
		}
	}
	reads := 0
	for bi := range b.Blocks {
		joined := false // a plain join earlier in the same block
		for _, st := range b.Blocks[bi].Stmts {
			if st.Kind == mir.StJoin && !st.Cancel {
				joined = true
			}
			if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvDiscriminant && st.Rvalue.Place.Local == sv {
				ps := b.Proj(st.Rvalue.Place)
				if len(ps) != 1 || ps[0].Kind != mir.ProjField || ps[0].A != 1 {
					t.Fatalf("err read is not field 1 of the scope local: %+v", ps)
				}
				if !joined {
					t.Fatalf("err read not right after the fall-through join:\n%s", f)
				}
				reads++
			}
		}
	}
	if reads != 1 {
		t.Fatalf("want one err read, got %d:\n%s", reads, f)
	}
	wantIn(t, f, "join _", "Discriminant", "return try")
	// With Env.Known, Validate's M3 reads Scope[E] as {hdr: Scope, err:
	// E!void}, so the err read (field 1, ErrorUnion(E, void)) and the whole
	// body validate.
	if err := validate("Run"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := validate("Work"); err != nil {
		t.Fatalf("Work: %v", err)
	}
	// The same source through the ordinary path (Validate on in Lower).
	w := newWorld(t, true)
	m := w.add("m", opt{}, tryScopeSrc)
	m.wantClean(t)
	lowerModule(t, w, m)

	// A block whose end is never reached (its last statement returns) has
	// no fall-through, so no plain join and no err read.
	l2, validate2 := lowerNoValidate(t, `error E
Work(): E!void {
}
Run(io: Io): E!void {
	try scope s := io.ScopeOf[E]() {
		s.go Work()
		return
	}
}
`)
	r := l2.print("Run")
	if count(r, "join _") != 1 || count(r, " cancel") != 1 || strings.Contains(r, "Discriminant") {
		t.Fatalf("a block that returns at its end: want one cancelling join and no err read:\n%s", r)
	}
	if err := validate2("Run"); err != nil {
		t.Fatalf("a block that returns at its end: %v", err)
	}
}
