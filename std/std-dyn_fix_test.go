// Why: gen dyn.Of writes code from a user's interface, so a user's names
// and types must never make it fail inside std.
// What: the TestDyn_ tests: keyword method names, parameters named like
// the output's locals, Self in a fn type, and a DeviceDyn in a global.
// How: each interface is checked, some built and run in dev and release; a
// diagnostic must land at the interface, never in std (E0901, E0905).

package std

import (
	"strings"
	"testing"
)

// keywordIface has a method for each of several reserved words, and two
// contextual words; every one must forward with no diagnostic.
const keywordIface = `import std.dyn

interface Log {
    Error(mut self, msg: str)
    Is(&self): bool
    Type(&self): int
    Match(&self, x: int): int
    Return(&self): int
    Go(mut self)
    Fn(&self): int
    Result(&self): int
    From(&self): int
}

gen dyn.Of(Log)
`

// TestDyn_KeywordSlots: the table slot of a method whose snake_case is a
// keyword or a contextual word takes an underscore (error_, is_), the
// forwarding method keeps the method's own name, and the output checks.
func TestDyn_KeywordSlots(t *testing.T) {
	t.Parallel()
	out, status, view := dynView(t, keywordIface+"\nexport Main(sys: &System) {\n}\n")
	if status != 0 || len(diagCodes(out)) != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	for _, want := range []string{
		"error_: fn(mut dyn.Ref, str),",
		"is_: fn(&dyn.Ref): bool,",
		"type_: fn(&dyn.Ref): int,",
		"match_: fn(&dyn.Ref, int): int,",
		"return_: fn(&dyn.Ref): int,",
		"go_: fn(mut dyn.Ref),",
		"fn_: fn(&dyn.Ref): int,",
		"result_: fn(&dyn.Ref): int,",
		"from_: fn(&dyn.Ref): int\n",
		"error_: LogDyn.AdaptError[T],",
		"    Error(mut self, msg: str) {\n        f := self.table.error_\n        f(mut self.obj, msg)\n    }",
		"    Is(&self): bool {\n        f := self.table.is_\n        return f(&self.obj)\n    }",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("gen view lacks %q:\n%s", want, view)
		}
	}
}

// hygieneIface has parameters named after the output's locals: f (the
// forwarding method's fn local) as a callback, obj (the adapter's object),
// both with the next candidates taken (obj2, f2), a name that folds to obj
// (o_bj, E0206), and one named after the module the output names (dyn).
const hygieneIface = `import std.dyn

interface V {
    Visit(mut self, f: fn(&int): bool): int
    Put(mut self, obj: u64)
    Both(mut self, obj: u64, obj2: int, f: int, f2: int): int
    Fold(&self, o_bj: int, dyn: int): int
}

gen dyn.Of(V)
`

// TestDyn_HygienicLocals: the adapter's object parameter and the
// forwarding method's fn local take the first name no parameter folds to,
// and the output checks with no diagnostic (it was E0905 blaming std).
func TestDyn_HygienicLocals(t *testing.T) {
	t.Parallel()
	out, status, view := dynView(t, hygieneIface+"\nexport Main(sys: &System) {\n}\n")
	if status != 0 || len(diagCodes(out)) != 0 {
		t.Fatalf("mk check: exit %d\n%s", status, out)
	}
	for _, want := range []string{
		"VDyn.AdaptVisit[T: V](obj: mut dyn.Ref, f: fn(&int): bool): int {\n    return obj.Mut[T]().Visit(f)\n}",
		"VDyn.AdaptPut[T: V](obj2: mut dyn.Ref, obj: u64) {\n    obj2.Mut[T]().Put(obj)\n}",
		"VDyn.AdaptBoth[T: V](obj3: mut dyn.Ref, obj: u64, obj2: int, f: int, f2: int): int {",
		"VDyn.AdaptFold[T: V](obj2: &dyn.Ref, o_bj: int, dyn: int): int {",
		"    Visit(mut self, f: fn(&int): bool): int {\n        f2 := self.table.visit\n        return f2(mut self.obj, f)\n    }",
		"        f3 := self.table.both\n        return f3(mut self.obj, obj, obj2, f, f2)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("gen view lacks %q:\n%s", want, view)
		}
	}
}

// TestDyn_HygienicRuns: the same interface built and run, dev and
// release: each call reaches its implementation with its arguments, a
// callback named f among them, and a method named Error forwards.
func TestDyn_HygienicRuns(t *testing.T) {
	t.Parallel()
	src := hygieneIface + `
struct Bag {
    xs: [4]int
    sum: u64
}

impl V for Bag {
    Visit(mut self, f: fn(&int): bool): int {
        var n := 0
        for i in 0..4 {
            if f(&self.xs[i]) {
                n += 1
            }
        }
        return n
    }
    Put(mut self, obj: u64) {
        self.sum += obj
    }
    Both(mut self, obj: u64, obj2: int, f: int, f2: int): int {
        self.sum += obj
        return obj2 * 1000 + f * 10 + f2
    }
    Fold(&self, o_bj: int, dyn: int): int {
        return o_bj - dyn + Cast[int](self.sum)
    }
}

interface Log {
    Error(mut self, msg: str)
    Is(&self): bool
}

gen dyn.Of(Log)

struct Count {
    n: int
}

impl Log for Count {
    Error(mut self, msg: str) {
        self.n += msg.Len()
    }
    Is(&self): bool {
        return self.n > 0
    }
}

Even(x: &int): bool {
    return *x % 2 == 0
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var b := Bag(xs: [1, 2, 3, 4], sum: 0)
    var v := VDyn.Of(mut b)
    evens := v.Visit(Even)
    v.Put(5)
    both := v.Both(2, 7, 3, 4)
    fold := v.Fold(10, 3)
    var c := Count(n: 0)
    var l := LogDyn.Of(mut c)
    was := l.Is()
    l.Error("abc")
    now := l.Is()
    try Print(mut out, "{evens} {both} {fold} {was} {now}\n", (evens, both, fold, was, now))
}
`
	for _, flags := range [][]string{nil, {"--release"}} {
		name := "dev"
		if flags != nil {
			name = "release"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// evens 2; both 7034; fold 10 - 3 + 7 = 14; Is false, then true.
			dynRun(t, src, 0, "2 7034 14 false true\n", "", flags...)
		})
	}
}

// TestDyn_FnSelfRefused: a method whose parameter or result is a fn type
// that mentions Self, at any depth, is E0903 at the interface ("mentions
// Self"), never E0905; a fn type without Self, and a name that only
// contains Self, are accepted.
func TestDyn_FnSelfRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, decl string }{
		{"fn-param", "interface I {\n    Each(&self, g: fn(&Self): int): int\n}"},
		{"fn-result", "interface I {\n    Maker(&self): fn(int): ?Self\n}"},
		{"fn-in-optional", "interface I {\n    Hook(mut self, h: ?fn(mut Self))\n}"},
		{"fn-in-slice", "interface I {\n    All(&self, hs: []fn([]Self): bool): int\n}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, _ := dynCheck(t, "import std.dyn\n\n"+c.decl+"\n\ngen dyn.Of(I)\n")
			if got := strings.Join(diagCodes(out), ","); got != "3 E0903" {
				t.Errorf("want one E0903 at the interface (line 3), got %q\n%s", got, out)
			}
			if !strings.Contains(out, "mentions Self") || strings.Contains(out, "E0905") {
				t.Errorf("want \"mentions Self\" and no E0905\n%s", out)
			}
		})
	}
	ok := "import std.dyn\n\nstruct SelfLike {\n    n: int\n}\n\ninterface I {\n    Each(&self, g: fn(&int): int): int\n    Like(&self, s: fn(&SelfLike): bool): bool\n}\n\ngen dyn.Of(I)\n"
	if out, status := dynCheck(t, ok); status != 0 || len(diagCodes(out)) != 0 {
		t.Errorf("fn types without Self: exit %d\n%s", status, out)
	}
}

// TestDyn_KernelGlobalNotShare: a global `var State: atomic.Spin[?Kernel]`,
// with Kernel holding a DeviceDyn, is E1002, since a DeviceDyn holds a
// dyn.Ref and a Ref's raw pointer is not Share (an erased object cannot
// promise its type is). A DeviceDyn of a local could not be stored in a
// global either. This is why doc/examples.md's kernel keeps the concrete
// Uart in its global and makes a DeviceDyn for the length of a call; this
// test changes if a dynamic object of a static may ever sit in a global.
func TestDyn_KernelGlobalNotShare(t *testing.T) {
	t.Parallel()
	src := "import std.atomic\n" + kernelDevice + "\n" + uartDecl + `
struct Kernel {
    console: DeviceDyn
    writes: int
}

var State: atomic.Spin[?Kernel] = atomic.Spin.New(none)
`
	out, _ := dynCheck(t, src)
	codes := diagCodes(out)
	found := false
	for _, c := range codes {
		found = found || strings.HasSuffix(c, " E1002")
	}
	if !found || !strings.Contains(out, "*u8") {
		t.Errorf("want E1002 naming the Ref's *u8, got %v\n%s", codes, out)
	}
}
