// Why: an interface method with generic parameters of its own (a Print-shaped
// method with a const format) once lost the call's own generic arguments when
// mono made the call direct, and mir.Validate failed on the instance.
// What: TestIfaceMethodOwnGenericsSource.
// How: lower keeps the call's own arguments on the interface call and mono
// appends them to the impl's; the test collects from source and validates.

package mono

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

func TestIfaceMethodOwnGenericsSource(t *testing.T) {
	src := `interface Pr {
    P[A](&self, const k: int, x: A): int
}

struct S {
    n: int
}

impl Pr for S {
    P[A](&self, const k: int, x: A): int {
        return self.n + k
    }
}

Use[T: Pr](t: &T): int {
    return t.P(1, 5) + t.P(2, true)
}

Run(): int {
    s := S(n: 3)
    return Use(&s)
}
`
	sw := newSrcWorld(t, src)
	pr, s := sw.item(t, "Pr"), sw.item(t, "S")
	implP, ok := sw.items.ByKey(types.ImplMemberKey(types.ImplKey("m", "m.Pr", "m.S"), "P"))
	if !ok {
		t.Fatal("no impl method P")
	}
	sw.impls[implKey{pr, 0, sw.tab.Named(s, nil)}] = implP

	// The generic caller's interface calls carry the method's own argument.
	for _, blk := range sw.bodies[sw.item(t, "Use")].Blocks {
		if tm := blk.Term; tm.Kind == mir.TermCall && tm.Callee.Kind == mir.CallInterface && tm.Callee.Fn.Args.Len != 1 {
			t.Errorf("interface call carries %d own generic arguments, want 1", tm.Callee.Fn.Args.Len)
		}
	}

	in, ds := sw.collect(t, Options{Validate: true}, sw.limitRoots()...)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	ps := instancesOf(in, implP)
	if len(ps) != 2 {
		t.Fatalf("%d instances of S's P, want 2 (A = int, k = 1; A = bool, k = 2):\n%s", len(ps), summary(in))
	}
	seen := map[types.TypeID]bool{}
	for _, p := range ps {
		if p.Generics != 1 || len(p.Args) != 2 {
			t.Fatalf("instance %s: %d generics of %d arguments, want 1 of 2 (A, then k)", p.Name, p.Generics, len(p.Args))
		}
		seen[p.Args[0].Type] = true
	}
	if !seen[types.Int] || !seen[types.Bool] {
		t.Errorf("instances of P at %v, want int and bool:\n%s", seen, summary(in))
	}
}
