// Why: a fn literal passed as a const argument may read the enclosing
// function's locals, and later passes need its captures listed.
// What: tests that resolve records a literal's captures, their order, the
// FieldsLoop and ConstFn flags, and that it reports no E0609 for them.
// How: resolves small modules and spells each literal's captures with
// capString; whether a capture is allowed is check's decision, not resolve's.

package resolve

import (
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/types"
)

// infoOf is the ItemInfo of item key in b.
func (b *built) infoOf(t *testing.T, key string) *ItemInfo {
	t.Helper()
	id := b.w.item(key).ID
	for _, ii := range b.info.Items {
		if ii.Item == id {
			return ii
		}
	}
	t.Fatalf("no ItemInfo for %s", key)
	return nil
}

// capString spells a literal's captures: name, then Of's kind and A.
func (b *built) capString(ii *ItemInfo) string {
	var parts []string
	for _, c := range ii.Captures {
		s := b.in.Str(c.Name) + "=" + c.Of.Kind.String() + itoa(int(c.Of.A))
		if c.FieldsLoop {
			s += "+fields"
		}
		if c.ConstFn {
			s += "+constfn"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// TestCapturesRecorded: captures in byte order of the names, Of for
// locals, parameters, self, var locals, loop variables and scope handles,
// and a nested literal's chain into the outer set; uses are RefCapture.
func TestCapturesRecorded(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `struct P {
    v: int
}

Use(f: fn(): int): int {
    return f()
}

P.M(&self, zeta: int): int {
    var alpha := 1
    for i in 0..3 {
        _ = Use(fn(): int { return zeta + alpha + i + self.v })
    }
    return alpha
}

F(limit: int, k: int) {
    g := fn(x: int): bool {
        h := fn(): bool { return limit > k }
        return x < limit && h()
    }
    _ = g
}
`)
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.P:M#fn0")); got != "alpha=local3 i=local4 self=param1 zeta=param2" {
		t.Errorf("P.M#fn0 captures %q", got)
	}
	outer := b.infoOf(t, "main.F#fn0")
	if got := b.capString(outer); got != "k=param2 limit=param1" {
		t.Errorf("F#fn0 captures %q", got)
	}
	// The inner literal's Of points into the outer literal's sorted set.
	inner := b.infoOf(t, "main.F#fn0#fn0")
	if got := b.capString(inner); got != "k=capture0 limit=capture1" {
		t.Errorf("F#fn0#fn0 captures %q", got)
	}
	for _, c := range inner.Captures {
		if types.ItemID(c.Of.B) != outer.Item {
			t.Errorf("inner capture %s points at item %d, want the outer literal", b.in.Str(c.Name), c.Of.B)
		}
	}
	// Every use of a capture is RefCapture of its innermost literal, with
	// the sorted index of its name.
	refs := b.info.Files[0].Refs
	seen := 0
	ast.Inspect(b.files[0], func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || int(id.ID()) >= len(refs) || refs[id.ID()].Kind != RefCapture {
			return n != nil
		}
		r := refs[id.ID()]
		for _, ii := range b.info.Items {
			if ii.Item == types.ItemID(r.B) {
				if int(r.A) >= len(ii.Captures) || ii.Captures[r.A].Name != id.Name {
					t.Errorf("use %s: RefCapture %d of %d names %v", b.in.Str(id.Name), r.A, r.B, ii.Captures)
				}
				seen++
			}
		}
		return true
	})
	if seen != 7 {
		t.Errorf("%d RefCapture uses, want 7", seen)
	}
}

// TestNotCaptures: items, consts, globals, type parameters and const
// generics are never captures.
func TestNotCaptures(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `const Max = 3

var Count: int = 0

Helper(): int {
    return 1
}

Use(f: fn(): int): int {
    return f()
}

G[T, const N: int](x: T): int {
    return Use(fn(): int { return Max + Helper() + N + SizeOf[T]() })
}
`)
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.G#fn0")); got != "" {
		t.Errorf("G#fn0 captures %q, want none", got)
	}
}

// TestNoE0609InResolve: a literal that uses an enclosing local draws
// no diagnostic from resolve, whatever position it stands in; check decides
// (check's TestE0609_Above has the cases).
func TestNoE0609InResolve(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, "F(limit: int, k: int) {\n    g := fn(x: int): bool {\n        return x < limit && x > k && x != limit\n    }\n    _ = g\n}\n")
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.F#fn0")); got != "k=param2 limit=param1" {
		t.Errorf("captures %q", got)
	}
}

// TestCaptureFlags: FieldsLoop where the name is declared by or in the
// fields loop that contains the literal; ConstFn for a const fn parameter.
func TestCaptureFlags(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `Use(f: fn(): int): int {
    return f()
}

Sum(const less: fn(&int, &int): bool, const n: int, s: (a: int, b: int)) {
    outer := 1
    for name, value in fields(&s) {
        inner := 2
        _ = Use(fn(): int { return outer + inner + *value })
        _ = name
    }
    _ = Use(fn(): int {
        if less(&outer, &outer) {
            return n
        }
        return 0
    })
}
`)
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.Sum#fn0")); got != "inner=local7+fields outer=local4 value=local6+fields" {
		t.Errorf("Sum#fn0 captures %q", got)
	}
	if got := b.capString(b.infoOf(t, "main.Sum#fn1")); got != "less=param1+constfn n=param2 outer=local4" {
		t.Errorf("Sum#fn1 captures %q", got)
	}
}

// TestCaptureFlags_FieldsLoopOfOuterBody: a name declared in
// an outer literal that lies in a fields loop, captured by an inner literal,
// is no fields-loop capture: the outer literal is a unit of its own, checked
// once, so the name has one type. A name the loop's own body declares stays
// one, at every frame of the chain, and so does a name declared inside a
// fields loop of the outer literal's own body.
func TestCaptureFlags_FieldsLoopOfOuterBody(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `Use(f: fn(): int): int {
    return f()
}

Count(s: (a: int, b: int)): int {
    var n := 0
    for _, v in fields(&s) {
        y := 1
        n += Use(fn(): int {
            z := 3
            _ = Use(fn(): int { return z + y })
            for _, w in fields(&s) {
                q := 4
                _ = Use(fn(): int { return q + z })
                _ = w
            }
            return 0
        })
        _ = v
    }
    return n
}
`)
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.Count#fn0")); got != "s=param1 y=local4+fields" {
		t.Errorf("Count#fn0 captures %q", got)
	}
	if got := b.capString(b.infoOf(t, "main.Count#fn0#fn0")); got != "y=capture1+fields z=local1" {
		t.Errorf("Count#fn0#fn0 captures %q, want z with no fields flag (its body is the outer literal's)", got)
	}
	if got := b.capString(b.infoOf(t, "main.Count#fn0#fn1")); got != "q=local3+fields z=local1" {
		t.Errorf("Count#fn0#fn1 captures %q, want q flagged (the outer literal's own fields loop declares it)", got)
	}
}

// TestResultCaptured: result inside a literal of an ensures
// predicate is a capture of the literal, its Of the RefResult of the
// function; outside a literal it stays RefResult.
func TestResultCaptured(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `All(xs: []int, const keep: fn(&int): bool): bool {
    return true
}

Max(xs: []int): int
    ensures result >= 0 && All(xs, fn(x: &int): bool { return *x <= result })
{
    return 0
}
`)
	b.want(t)
	ii := b.infoOf(t, "main.Max#ensures0#fn0")
	if got := b.capString(ii); got != "result=result0" {
		t.Errorf("Max#ensures0#fn0 captures %q", got)
	}
	max := b.w.item("main.Max").ID
	for _, c := range ii.Captures {
		if b.in.Str(c.Name) == "result" && types.ItemID(c.Of.B) != max {
			t.Errorf("result's Of names item %d, want Max %d", c.Of.B, max)
		}
	}
}

// TestInvariantFieldCapturesSelf: a bare field name of an
// invariant inside a literal of the predicate is a field of the predicate's
// self, so the literal captures self (a nested literal through its outer
// one's capture); the name keeps its RefField.
func TestInvariantFieldCapturesSelf(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `Ap(const f: fn(): bool): bool {
    return f()
}

struct Span {
    lo: int
    hi: int
    invariant lo <= hi && Ap(fn(): bool { return lo < hi + 1 && Ap(fn(): bool { return hi > 0 }) })
}
`)
	b.want(t)
	if got := b.capString(b.infoOf(t, "main.Span#invariant0#fn0")); got != "self=param1" {
		t.Errorf("the invariant's literal captures %q, want self=param1", got)
	}
	if got := b.capString(b.infoOf(t, "main.Span#invariant0#fn0#fn0")); got != "self=capture0" {
		t.Errorf("the nested literal captures %q, want self=capture0", got)
	}
}

// TestNestedChainRepeatedUse: an inner literal that uses a name twice,
// inside an outer literal that captured another name first, points its Of
// at the name's sorted index in the outer set: the outer frame rewrites each
// inner capture once (a second rewrite through the permutation named ys).
func TestNestedChainRepeatedUse(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("main", opt{}, `Use(f: fn(): bool): bool {
    return f()
}

F(ys: int, origin: int): bool {
    return Use(fn(): bool {
        _ = ys
        _ = Use(fn(): bool { return origin < origin + 1 })
        return origin > 0
    })
}
`)
	b.want(t)
	outer := b.infoOf(t, "main.F#fn0")
	if got := b.capString(outer); got != "origin=param2 ys=param1" {
		t.Errorf("F#fn0 captures %q", got)
	}
	inner := b.infoOf(t, "main.F#fn0#fn0")
	if got := b.capString(inner); got != "origin=capture0" {
		t.Errorf("F#fn0#fn0 captures %q, want origin=capture0 (origin's index in the outer set)", got)
	}
}
