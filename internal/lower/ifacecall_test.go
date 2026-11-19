// Why: check's E0702 (an interface call on self in a non-generic
// interface-owned function) must reject exactly the bodies mir's M2 would
// reject, or an accepted body fails validation.
// What: TestIfaceCallLowersIsM2 and its helper lowerWithEnvs.
// How: lowers one generic body and validates it with each GenericCount,
// comparing the result with check.IfaceCallLowers.

package lower

import (
	"strings"
	"testing"

	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// lowerWithEnvs is lowerNoValidate returning the env each body validates
// with, so a test can validate a mutated body under a changed env.
func lowerWithEnvs(t *testing.T, src string) (*lowered, map[string]*mir.Env) {
	t.Helper()
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	out := &lowered{w: w, b: b, bodies: map[string]*mir.Body{}}
	cfg := w.lowerCfg()
	cfg.Validate = false
	envs := map[string]*mir.Env{}
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
		envs[k] = &mir.Env{Types: cfg.Types, Sig: in.Sig(), Callee: cfg.callee, Known: cfg.Known, Facts: in.Facts}
	}
	return out, envs
}

// TestIfaceCallLowersIsM2: a generic body's call through its bound
// (`v.Area()`, a CallInterface) validates; the same body with its
// GenericCount set to each of 0..3 fails M2 exactly when
// check.IfaceCallLowers(GenericCount) is false, and at 0 it is M2's call
// rule that fails it (mir/validate.go's CallInterface case: "interface
// call in an instance body"): the body's locals of a generic type are
// first made concrete (T to i64) and its signature dropped from the env,
// so no other M2 or M1 check answers before the call.
func TestIfaceCallLowersIsM2(t *testing.T) {
	l, envs := lowerWithEnvs(t, "interface Shape {\n    Area(&self): int\n}\n\nTwice[T: Shape](v: &T): int {\n    return v.Area() * 2\n}\n")
	b := l.body("Twice")
	env := envs["Twice"]
	calls := 0
	for i := range b.Blocks {
		if tm := b.Blocks[i].Term; tm.Kind == mir.TermCall && tm.Callee.Kind == mir.CallInterface {
			calls++
		}
	}
	if calls == 0 {
		t.Fatalf("Twice has no CallInterface:\n%s", l.print("Twice"))
	}
	if err := b.Validate(env); err != nil {
		t.Fatalf("Twice as lowered: %v", err)
	}
	orig := b.GenericCount
	defer func() { b.GenericCount = orig }()
	for n := uint32(0); n <= 3; n++ {
		b.GenericCount = n
		err := b.Validate(env)
		m2 := err != nil && strings.Contains(err.Error(), "M2:")
		if m2 != !check.IfaceCallLowers(int(n)) {
			t.Errorf("GenericCount %d: M2 %v (%v), IfaceCallLowers %v", n, m2, err, check.IfaceCallLowers(int(n)))
		}
	}

	// The call rule itself, at GenericCount 0.
	tab := env.Types
	var concrete func(ty types.TypeID) types.TypeID
	concrete = func(ty types.TypeID) types.TypeID {
		switch x := tab.Get(ty); x.Kind {
		case types.KParam:
			return types.I64
		case types.KRef:
			return tab.Ref(concrete(x.Elem))
		}
		return ty
	}
	saved := make([]types.TypeID, len(b.Locals))
	for i := range b.Locals {
		saved[i] = b.Locals[i].Type
		b.Locals[i].Type = concrete(b.Locals[i].Type)
	}
	defer func() {
		for i := range b.Locals {
			b.Locals[i].Type = saved[i]
		}
	}()
	noSig := *env
	noSig.Sig = nil
	b.GenericCount = 0
	err := b.Validate(&noSig)
	if err == nil || !strings.Contains(err.Error(), "M2:") || !strings.Contains(err.Error(), "interface call in an instance body") {
		t.Errorf("GenericCount 0 with concrete locals: %v, want M2's interface call in an instance body", err)
	}
	b.GenericCount = orig
	if err := b.Validate(&noSig); err != nil {
		t.Errorf("GenericCount %d with concrete locals: %v, want valid (the mutation alone breaks nothing)", orig, err)
	}
}
