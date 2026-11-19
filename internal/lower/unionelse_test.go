// Why: `e else [x] { H }` on an E!T value that is no call (a parameter, a
// local) was an ICE with x ("use of a binding before its declaration")
// and, without x, a downcast to some (rejected by mir in a value or a
// return) or a silent miscompile in a dropped statement (the handler ran
// for ok).
// What: TestElse* and wantUnionElse.
// How: it lowers as matchUnion reads an E!T, not as a ?T: a switch on the
// discriminant (0 for ok), the SelOk payload on the success edge, the SelErr
// error bound to x in an else handler; eval runs each form.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

const unionSrc = `error Bad(code: int)

Get(n: int): Bad!int {
	if n < 0 {
		fail Bad(code: n)
	}
	return n
}

GetV(n: int): Bad!void {
	if n < 0 {
		fail Bad(code: n)
	}
}
`

// wantUnionElse holds the E!T shape of an else on a non-call value in body
// key: the discriminant, the ok payload, and never the ?T path's some.
func wantUnionElse(t *testing.T, l *lowered, key string, frags ...string) string {
	t.Helper()
	s := l.print(key)
	wantIn(t, s, append([]string{"Discriminant("}, frags...)...)
	if strings.Contains(s, " as some") {
		t.Errorf("%s: an E!T value lowered as a ?T:\n%s", key, s)
	}
	return s
}

// TestElseBindingNoICE is diag7's ice.mk (internal/driver
// TestElseBindingNoICE) word for word: a handler that binds the error of
// a parameter, uses it, and returns.
func TestElseBindingNoICE(t *testing.T) {
	l := lowerSrc(t, `error Bad

Code(r: Bad!int): int {
    v := r else e {
        _ = e
        return 1
    }
    return v
}
`)
	s := wantUnionElse(t, l, "Code", " as ok).0", " as err)")
	found := false
	for _, lc := range l.body("Code").Locals {
		found = found || lc.Kind == mir.LocalUser && l.w.in.Str(lc.Name) == "e"
	}
	if !found {
		t.Errorf("e is no user local:\n%s", s)
	}
}

// TestElseNamedStatement: a named handler that does not fall through,
// on a parameter and on a local, binds e from the SelErr downcast.
func TestElseNamedStatement(t *testing.T) {
	l := lowerSrc(t, unionSrc+`Param(r: Bad!int): int {
	v := r else e {
		_ = e
		return -1
	}
	return v
}

Local(n: int): int {
	var r: Bad!int = Get(n)
	v := r else e {
		_ = e
		return -1
	}
	return v
}

Void(r: Bad!void): int {
	r else e {
		_ = e
		return -1
	}
	return 1
}
`)
	for _, k := range []string{"Param", "Local"} {
		wantUnionElse(t, l, k, " as ok).0", " as err)")
	}
	wantUnionElse(t, l, "Void", " as err)")
}

// TestElsePlainStatement: an unnamed handler, dropped (`_ = r else
// { }`, the shape that miscompiled) and returning.
func TestElsePlainStatement(t *testing.T) {
	l := lowerSrc(t, unionSrc+`Drop(r: Bad!int): int {
	var n := 0
	_ = r else {
		n = 1
	}
	return n
}

Ret(r: Bad!int): int {
	v := r else {
		return -1
	}
	return v
}

Void(r: Bad!void): int {
	var n := 0
	r else {
		n = 1
	}
	return n
}
`)
	wantUnionElse(t, l, "Drop")
	wantUnionElse(t, l, "Ret", " as ok).0")
	wantUnionElse(t, l, "Void")
}

// TestElseValue: a handler that yields a value, named and not, on a
// parameter and a local.
func TestElseValue(t *testing.T) {
	l := lowerSrc(t, unionSrc+`Named(r: Bad!int): int {
	v := r else e {
		_ = e
		9
	}
	return v
}

Bare(r: Bad!int): int {
	return r else { 8 }
}

Local(n: int): int {
	var r: Bad!int = Get(n)
	v := r else e {
		_ = e
		9
	}
	return v
}
`)
	wantUnionElse(t, l, "Named", " as ok).0", " as err)", "= int 9")
	wantUnionElse(t, l, "Bare", " as ok).0", "= int 8")
	wantUnionElse(t, l, "Local", " as ok).0", " as err)", "= int 9")
}

// TestElseRuns evaluates each shape for an ok and an err value: the ok
// edge gives the payload, the handler runs only for err, and a named
// handler sees the error (matched on its payload).
func TestElseRuns(t *testing.T) {
	shapes := map[string]string{
		"param value named": `F(r: Bad!int): int {
	v := r else e {
		match e {
			Bad(code: c) {
				100 + c
			}
		}
	}
	return v
}
H(): int {
	return F(Get(N)) * 1000 + F(Get(M))
}
`,
		"param value plain": `F(r: Bad!int): int {
	return r else { 100 }
}
H(): int {
	return F(Get(N)) * 1000 + F(Get(M))
}
`,
		"param return named": `F(r: Bad!int): int {
	v := r else e {
		match e {
			Bad(code: c) {
				return 200 + c
			}
		}
	}
	return v
}
H(): int {
	return F(Get(N)) * 1000 + F(Get(M))
}
`,
		"param return plain": `F(r: Bad!int): int {
	v := r else {
		return 200
	}
	return v
}
H(): int {
	return F(Get(N)) * 1000 + F(Get(M))
}
`,
		"param drop plain": `F(r: Bad!int): int {
	var n := 5
	_ = r else {
		n = 300
	}
	return n
}
H(): int {
	return F(Get(N)) * 1000 + F(Get(M))
}
`,
		"void stmt plain": `F(r: Bad!void): int {
	var n := 5
	r else {
		n = 300
	}
	return n
}
H(): int {
	return F(GetV(N)) * 1000 + F(GetV(M))
}
`,
		"local value named": `F(k: int): int {
	var r: Bad!int = Get(k)
	v := r else e {
		match e {
			Bad(code: c) {
				100 + c
			}
		}
	}
	return v
}
H(): int {
	return F(N) * 1000 + F(M)
}
`,
		"local return plain": `F(k: int): int {
	var r: Bad!int = Get(k)
	v := r else {
		return 200
	}
	return v
}
H(): int {
	return F(N) * 1000 + F(M)
}
`,
	}
	// N is an ok value (7), M an err (Bad(code: -3)).
	want := map[string]int64{
		"param value named":  7*1000 + 97,
		"param value plain":  7*1000 + 100,
		"param return named": 7*1000 + 197,
		"param return plain": 7*1000 + 200,
		"param drop plain":   5*1000 + 300,
		"void stmt plain":    5*1000 + 300,
		"local value named":  7*1000 + 97,
		"local return plain": 7*1000 + 200,
	}
	for name, body := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := unionSrc + strings.NewReplacer("(N)", "(7)", "(M)", "(-3)").Replace(body)
			got, err := evalH(t, src)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got != want[name] {
				t.Errorf("H() = %d, want %d", got, want[name])
			}
		})
	}
}

// TestElseMovesLinear: a non-plain E!T parameter is consumed once, as a
// call result is: moved into the temporary the switch reads, its ok payload
// moved out on the success edge, and its error moved into e.
func TestElseMovesLinear(t *testing.T) {
	l := lowerSrc(t, resSrc+`Named(r: E!Res) {
	v := r else e {
		_ = e
		return
	}
	v.Abandon()
}

Bare(r: E!Res): int {
	v := r else {
		return 1
	}
	v.Abandon()
	return 0
}
`)
	for _, k := range []string{"Named", "Bare"} {
		s := wantUnionElse(t, l, k)
		wantIn(t, s, "= move _1\n", "Discriminant(", " = move (_", " as ok).0")
		if strings.Count(s, "move _1\n") != 1 || strings.Contains(s, "copy _1\n") {
			t.Errorf("%s: r is not taken exactly once:\n%s", k, s)
		}
	}
	wantIn(t, l.print("Named"), " as ok).0", " as err)")
}

// TestElseRunsLinear evaluates the linear shape of TestElseMovesLinear
// for an ok and an err value: the payload moved out on the ok edge is the
// one the call made.
func TestElseRunsLinear(t *testing.T) {
	got, err := evalH(t, `error Bad(code: int)
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
	v := r else e {
		match e {
			Bad(code: c) {
				return 100 + c
			}
		}
	}
	return v.Done()
}
H(): int {
	return F(Open(4)) * 1000 + F(Open(-3))
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != 4*1000+97 {
		t.Errorf("H() = %d, want %d", got, 4*1000+97)
	}
}
