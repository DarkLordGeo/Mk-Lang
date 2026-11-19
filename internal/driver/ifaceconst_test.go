// Why: an interface method with its own const generic, called through a
// bound with explicit arguments (x.M[5]()), failed lower's mir.Validate
// with a terminator type out of range: lower read Self only from an x.M
// callee, not x.M[A], so the interface call had no SelfType.
// What: TestIfaceConstGenericThroughBound.
// How: builds and runs the shapes, so mono's direct call is judged too; the
// seed internal/corpus/testdata/fuzz/FuzzCheck/lower_iface_const_generic
// keeps check and lower clean. The SIMD side (Reinterpret, WidenLo) is
// judged by tests/programs/panics/simd_*_unjudged.mk,
// simd_*_restated_closed.mk (a restatement the check demands and does not
// judge: the entry check in dev, codegen's guard in release) and, for a
// size or lane-count clause over a bare lane type parameter, which the
// caller restates, by tests/conformance/codes/E0706_simd_*_restated.mk.

package driver

import (
	"strings"
	"testing"
)

const cice2IfaceConstGeneric = `interface I {
    M[const K: int](&self): int
    G[U, const K: int](&self, u: U): U
}

struct S {
    a: int
}

impl I for S {
    M[const K: int](&self): int {
        return K + self.a
    }
    G[U, const K: int](&self, u: U): U {
        return u
    }
}

struct Box[X] {
    x: X
    a: int
}

impl[X] I for Box[X] {
    M[const K: int](&self): int {
        return K * self.a
    }
    G[U, const K: int](&self, u: U): U {
        return u
    }
}

H[T: I](x: &T): int {
    return x.M[5]() + x.M[30]()
}

J[T: I, const N: int](x: &T): int {
    return x.M[N]() + x.G[int, N](N)
}

export Main(sys: &System): int {
    s := S(a: 1)
    b := Box[bool](x: true, a: 7)
    if H[S](&s) != 37 {
        return 1
    }
    if J[S, 100](&s) != 201 {
        return 2
    }
    if H[Box[bool]](&b) != 245 {
        return 3
    }
    if J[Box[bool], 3](&b) != 24 {
        return 4
    }
    return 0
}
`

func TestIfaceConstGenericThroughBound(t *testing.T) {
	c := unitSession(t)
	out := c.CheckUnit(UnitInput{Name: "cice2#1", Mode: UnitRun, Files: []UnitFile{{"main.mk", cice2IfaceConstGeneric}}})
	if len(out.Report.Diags) > 0 {
		skipKnownBlockers(t, Result{Report: out.Report, Sources: out.Sources})
		t.Fatalf("cice2#1:\n%s", out.Report.Text(out.Sources))
	}
	if !out.Built || !out.Ran || out.RunStatus != 0 {
		t.Errorf("Built %v Ran %v RunStatus %d RunSkipped %q; want built, run, status 0 (1 to 4 name the wrong call)",
			out.Built, out.Ran, out.RunStatus, strings.TrimSpace(out.RunSkipped))
	}
}
