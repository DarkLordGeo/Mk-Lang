// Why: the MIR validator and the trim pass must both hold on every body the
// corpus lowers.
// What: TestValidateEveryGolden, TestTrimKeepsBorrowedAndNonPlain.
// How: lowers every corpus unit with Config.Validate off, then runs
// mir.(*Body).Validate with the full Env, with and without the trim pass.

package lower

import (
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// TestValidateEveryGolden lowers every corpus unit with Config.Validate off,
// then runs mir.(*Body).Validate itself with the full Env (Types, Sig,
// Callee, Facts, Known), with and without the trim pass.
func TestValidateEveryGolden(t *testing.T) {
	core, pre := corpusPrelude(t), testPrelude(t)
	n := 0
	for _, u := range corpusUnits(t) {
		w, b, ok := corpusWorld(t, core, pre, u)
		if !ok {
			continue
		}
		for _, trimmed := range []bool{false, true} {
			cfg := w.lowerCfg()
			cfg.Validate, cfg.Trim = false, trimmed
			for _, id := range b.m.Units() {
				it := w.items.Get(id)
				var body *check.Body
				if ie := source.Guard(it.Key.String(), it.Span, func() { body = w.c.Check(b.m, id) }); ie != nil || body == nil || len(body.Diags) > 0 {
					continue
				}
				in := FromCheck(cfg, w.c, body)
				var mb *mir.Body
				if ie := source.Guard(it.Key.String(), it.Span, func() { mb = Lower(cfg, in) }); ie != nil {
					continue // the pending ICEs: TestCorpusLowersAndValidates judges them
				}
				env := &mir.Env{Types: w.tab, Sig: in.Sig(), Callee: cfg.callee, Known: cfg.Known,
					Facts: func(t types.TypeID) (types.Facts, bool) { return in.Facts(t) }}
				if err := mb.Validate(env); err != nil {
					t.Errorf("%s %s (trim %v): %v", u.key, it.Key, trimmed, err)
				}
				n++
			}
		}
	}
	if n < 400 {
		t.Fatalf("only %d bodies validated", n)
	}
	t.Logf("%d bodies validated (each with and without trim)", n)
}

func TestTrimKeepsBorrowedAndNonPlain(t *testing.T) {
	src := resSrc + `F(r: Res): int {
	a := 1
	b := 2
	p := &b
	r.Abandon()
	return a + *p
}
`
	full := lowerSrc(t, src).print("F")
	l := lowerWith(t, src, func(c *Config) { c.Trim = true })
	trimmed := l.print("F")
	b := l.body("F")
	// a (plain, never borrowed) loses its scope end; b (borrowed) keeps it;
	// p (plain, never a borrow root) loses it.
	for _, want := range []struct {
		name string
		keep bool
	}{{"a", false}, {"b", true}, {"p", false}} {
		var loc mir.LocalID = mir.NoLocal
		for i, lc := range b.Locals {
			if l.w.in.Str(lc.Name) == want.name {
				loc = mir.LocalID(i)
			}
		}
		if loc == mir.NoLocal {
			t.Fatalf("no local %s", want.name)
		}
		end := "scope_end _" + itoa(int(loc)) + "\n"
		if !strings.Contains(full, end) {
			t.Fatalf("untrimmed body lacks %q:\n%s", end, full)
		}
		if strings.Contains(trimmed, end) != want.keep {
			t.Fatalf("trim of %s (keep %v):\n%s", want.name, want.keep, trimmed)
		}
	}
	// A non-plain local keeps its scope end: Res-typed temporaries.
	r := lowerWith(t, resSrc+`G() {
	x := Res(h: 1)
	x.Abandon()
}
`, func(c *Config) { c.Trim = true }).print("G")
	if !strings.Contains(r, "scope_end _1") {
		t.Fatalf("the linear local lost its scope end:\n%s", r)
	}
}
