// Why: scopes, tasks, unsafe, extern and variadics carry check's Send,
// Share and unsafe rules, which guard against data races and raw access.
// What: TestE1101, TestE1102_TwoSitesOneModule, TestE1103, TestE1001 to
// TestE1003, TestScopeErrors, TestScopeLayout, TestE1101_RawAndPacked.
// How: checks small bodies and compares the diagnostic codes.

package check

import "testing"

func TestE1103(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"return *p", "F(p: *int): int {\n\treturn *p\n}\n", "E1103@2:9"},
		{"in unsafe", "F(p: *int): int {\n\tunsafe \"p is valid\" {\n\t\treturn *p\n\t}\n}\n", ""},
		{"field through a raw pointer", pointSrc + "F(p: *Point): int {\n\treturn p.x\n}\n", "E1103@7:9"},
		{"passing needs nothing", "G(p: *int) {\n}\n\nF(p: *int) {\n\tG(p)\n}\n", ""},
	})
}

func TestE1101(t *testing.T) {
	const ext = "extern Puts(s: *u8): i32\n\nextern Printf(f: *u8, ...): i32\n\nexport extern Twice(n: i32): i32 {\n\treturn n * 2\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"bodiless extern", ext + "F(p: *u8): i32 {\n\treturn Puts(p)\n}\n", "E1101@10:9"},
		{"in unsafe", ext + "F(p: *u8): i32 {\n\tunsafe \"C call\" {\n\t\treturn Puts(p)\n\t}\n}\n", ""},
		{"variadic", ext + "F(p: *u8): i32 {\n\tunsafe \"C call\" {\n\t\treturn Printf(p, 1, 2.5, true)\n\t}\n}\n", ""},
		{"extern with a body", ext + "F(): i32 {\n\treturn Twice(2)\n}\n", ""},
		{"variadic str", ext + "F(p: *u8): i32 {\n\tunsafe \"C call\" {\n\t\treturn Printf(p, \"x\")\n\t}\n}\n", "E0301@11:20"},
	})
}

const scopeSrc = "error A\n\nstruct R {\n\tp: *int\n}\n\nWork(c: &Cell[int]) {\n}\n\nTake(r: R) {\n}\n\nPlainWork(n: int) {\n}\n\n"

func TestE1001(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"Send argument", scopeSrc + "F(s: &Scope, n: int) {\n\ts.go PlainWork(n)\n}\n", ""},
		{"not Send", scopeSrc + "F(s: &Scope, r: R) {\n\ts.go Take(r)\n}\n", "E1001@17:12"},
	})
}

func TestE1002(t *testing.T) {
	runBodyCases(t, []bodyCase{
		// A bound's method with a const generic, `s.go x.Go[1]()` twice on a
		// Ctr holding a Cell, raced in safe code before the method's receiver
		// was judged here.
		{"bound method receiver, generic arguments, a Cell implementer", "interface Run {\n\tGo[const V: int](&self)\n}\n\nstruct Ctr {\n\tc: Cell[int]\n}\n\n" +
			"impl Run for Ctr {\n\tGo[const V: int](&self) {\n\t\tself.c.Store(self.c.Load() + V)\n\t}\n}\n\n" +
			"F[T: Run](s: &Scope, x: &T) {\n\ts.go x.Go[1]()\n\ts.go x.Go[1]()\n}\n\nG(s: &Scope, k: &Ctr) {\n\tF[Ctr](s, k)\n}\n", "E1002@16:7 E1002@17:7"},
		{"&Cell argument", scopeSrc + "F(s: &Scope, c: &Cell[int]) {\n\ts.go Work(c)\n}\n", "E1002@17:12"},
		{"go on a non-scope", scopeSrc + "F(n: int) {\n\tn.go PlainWork(n)\n}\n", "E0301@17:2"},
		// The receiver of a bound's method crosses to the task.
		{"bound method receiver", "interface Run {\n\tGo(&self)\n}\n\nF[T: Run](s: &Scope, x: &T) {\n\ts.go x.Go()\n}\n", "E1002@6:7"},
		{"bound method receiver Share", "interface Run {\n\tGo(&self)\n}\n\nF[T: Run + Share](s: &Scope, x: &T) {\n\ts.go x.Go()\n}\n", ""},
		// With explicit generic arguments the callee is
		// an IndexExpr over the member, and its receiver crosses too.
		{"bound method receiver, generic arguments", "interface Run {\n\tGo[const K: int](&self)\n}\n\nF[T: Run](s: &Scope, x: &T) {\n\ts.go x.Go[1]()\n}\n", "E1002@6:7"},
		{"bound method receiver Share, generic arguments", "interface Run {\n\tGo[const K: int](&self)\n}\n\nF[T: Run + Share](s: &Scope, x: &T) {\n\ts.go x.Go[1]()\n}\n", ""},
		{"method receiver, generic arguments", scopeSrc + "struct Ctr {\n\tc: Cell[int]\n}\n\nCtr.Bump[const K: int](&self) {\n\tself.c.Store(self.c.Load() + K)\n}\n\nF(s: &Scope, k: &Ctr) {\n\ts.go k.Bump[1]()\n}\n", "E1002@25:7"},
		// An auto-borrowed `&self` receiver passes
		// &Ctr, not the receiver's own type Ctr (which is Send), so it is
		// judged at &Ctr: a local value, and a field place through a
		// reference parameter, with and without generic arguments.
		{"local value receiver", scopeSrc + ctrSrc + "F(s: &Scope) {\n\tk := Ctr(c: Cell[int].New(0))\n\ts.go k.BumpPlain()\n}\n", "E1002@30:7"},
		{"local value receiver, generic arguments", scopeSrc + ctrSrc + "F(s: &Scope) {\n\tk := Ctr(c: Cell[int].New(0))\n\ts.go k.Bump[1]()\n}\n", "E1002@30:7"},
		{"field of a reference parameter", scopeSrc + ctrSrc + "struct Holder {\n\tctr: Ctr\n}\n\nF(s: &Scope, h: &Holder) {\n\ts.go h.ctr.BumpPlain()\n}\n", "E1002@33:7"},
		{"field of a reference parameter, generic arguments", scopeSrc + ctrSrc + "struct Holder {\n\tctr: Ctr\n}\n\nF(s: &Scope, h: &Holder) {\n\ts.go h.ctr.Bump[1]()\n}\n", "E1002@33:7"},
		// A reference receiver is reborrowed too (Deref 1): k: mut Ctr
		// passes &Ctr to a `&self` method, and `mut Ctr` itself is Send.
		{"mut reference receiver", scopeSrc + ctrSrc + "F(s: &Scope, k: mut Ctr) {\n\ts.go k.BumpPlain()\n}\n", "E1002@29:7"},
		{"mut reference receiver, generic arguments", scopeSrc + ctrSrc + "F(s: &Scope, k: mut Ctr) {\n\ts.go k.Bump[1]()\n}\n", "E1002@29:7"},
		// Controls: a Share receiver type passes the same way, and a value
		// receiver (self by value) still crosses as the value.
		{"local value receiver Share", scopeSrc + "struct P {\n\tn: int\n}\n\nP.Get(&self) {\n}\n\nF(s: &Scope) {\n\tp := P(n: 1)\n\ts.go p.Get()\n}\n", ""},
		{"local value receiver mut self", scopeSrc + "struct P {\n\tn: int\n}\n\nP.Inc(mut self) {\n\tself.n = self.n + 1\n}\n\nF(s: &Scope) {\n\tp := P(n: 1)\n\ts.go p.Inc()\n}\n", ""},
		{"local value receiver mut self, not Share", scopeSrc + ctrSrc + "Ctr.Set(mut self) {\n\tself.c.Store(2)\n}\n\nF(s: &Scope) {\n\tk := Ctr(c: Cell[int].New(0))\n\ts.go k.Set()\n}\n", ""},
		{"bound method value receiver", "interface Run {\n\tGo(&self)\n}\n\nF[T: Run](s: &Scope, x: T) {\n\ts.go x.Go()\n}\n", "E1002@6:7"},
		{"bound method value receiver Share", "interface Run {\n\tGo(&self)\n}\n\nF[T: Run + Share](s: &Scope, x: T) {\n\ts.go x.Go()\n}\n", ""},
		{"local value receiver by value", scopeSrc + "struct P {\n\tn: int\n}\n\nP.Take(self) {\n}\n\nF(s: &Scope) {\n\tp := P(n: 1)\n\ts.go p.Take()\n}\n", ""},
	})
}

// ctrSrc is a Send, not Share, type with a plain and a generic `&self`
// method.
const ctrSrc = "struct Ctr {\n\tc: Cell[int]\n}\n\nCtr.BumpPlain(&self) {\n\tself.c.Store(self.c.Load() + 1)\n}\n\nCtr.Bump[const K: int](&self) {\n\tself.c.Store(self.c.Load() + K)\n}\n\n"

func TestE1003(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"bound", scopeSrc + "F(s: &Scope) {\n\tx := *s\n}\n", "E1003@17:7"},
		{"passed by reference", scopeSrc + "Watch(s: &Scope) {\n}\n\nF(s: &Scope) {\n\tWatch(s)\n}\n", ""},
		{"io.Scope bound", "F(io: Io) {\n\tx := io.Scope()\n}\n", "E1003@2:7"},
		{"io.Scope dropped", "F(io: Io) {\n\tio.Scope()\n}\n", "E1003@2:2"},
		{"passed by value", "Keep(s: Scope) {\n}\n\nF(io: Io) {\n\tscope s := io.Scope() {\n\t\tKeep(s)\n\t}\n}\n", "E1003@6:8"},
		{"stored in a field", "struct H {\n\ts: Scope\n}\n\nF(io: Io) {\n\tscope s := io.Scope() {\n\t\th := H(s: s)\n\t}\n}\n", "E1003@7:13"},
		{"returned", "F(s: &Scope): Scope {\n\treturn *s\n}\n", "E1003@2:9"},
		{"as the initializer", "Tick() {\n}\n\nF(io: Io) {\n\tscope s := io.Scope() {\n\t\ts.go Tick()\n\t\ts.Cancel()\n\t}\n}\n", ""},
	})
}

const scopeErrSrc = "error E1\n\nerror E2\n\nTick() {\n}\n\nClear(): E1!void {\n}\n\nBoth(): (E1 | E2)!void {\n}\n\nCount(): int {\n\treturn 1\n}\n\n"

func TestScopeErrors(t *testing.T) {
	runBodyCases(t, []bodyCase{
		// A task under Scope[E] returns E!void.
		{"reference block", scopeErrSrc + "Run(io: Io): E1!void {\n\ttry scope s := io.ScopeOf[E1]() {\n\t\ts.go Clear()\n\t\ts.go Tick()\n\t}\n}\n", "E0301@21:8"},
		{"Scope[E] without try", scopeErrSrc + "Run(io: Io): E1!void {\n\tscope s := io.ScopeOf[E1]() {\n\t\ts.go Clear()\n\t}\n}\n", "E0802@19:13"},
		{"try scope over Scope", scopeErrSrc + "Run(io: Io) {\n\ttry scope s := io.Scope() {\n\t\ts.go Tick()\n\t}\n}\n", "E0804@19:2"},
		{"task set too wide", scopeErrSrc + "Run(io: Io): E1!void {\n\ttry scope s := io.ScopeOf[E1]() {\n\t\ts.go Both()\n\t}\n}\n", "E0801@20:8"},
		{"scope set not the function's", scopeErrSrc + "Run(io: Io) {\n\ttry scope s := io.ScopeOf[E1]() {\n\t\ts.go Clear()\n\t}\n}\n", "E0801@19:2"},
		{"a value result under Scope", scopeErrSrc + "Run(io: Io) {\n\tscope s := io.Scope() {\n\t\ts.go Count()\n\t}\n}\n", "E0301@20:8"},
		{"fallible task under Scope", scopeErrSrc + "Run(io: Io) {\n\tscope s := io.Scope() {\n\t\ts.go Clear()\n\t}\n}\n", "E0301@20:8"},
		{"accepted", scopeErrSrc + "Run(io: Io): (E1 | E2)!void {\n\ttry scope s := io.ScopeOf[E1 | E2]() {\n\t\ts.go Clear()\n\t\ts.go Both()\n\t}\n}\n", ""},
	})
}

func TestScopeLayout(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, "error E1\n\nF(s: &Scope, t: &Scope[E1]) {\n}\n")
	_ = b
	sc := w.items.Get(w.c.known.Scope)
	plain := w.tab.Named(sc.ID, nil)
	if l := w.c.Layout(plain); l.Shape.Size != 128 || l.Shape.Align != 8 {
		t.Fatalf("Scope layout %+v, want 128/8", l.Shape)
	}
	sig := b.sigOf("F")
	withE := w.tab.Get(sig.Params[1].Type).Elem
	l := w.c.Layout(withE)
	if len(l.Fields) != 2 || l.Fields[1].Offset != 128 {
		t.Fatalf("Scope[E1] layout %+v, want err at 128", l)
	}
	if w.c.Facts(plain).Freeze || w.c.Facts(withE).Freeze {
		t.Fatalf("a Scope is not Freeze")
	}
}

func TestE1101_RawAndPacked(t *testing.T) {
	const raw = "export Read[T](p: *T): T\nexport Of[T](r: &T): *T\nexport Str(p: *u8, n: int): str\n"
	rawCase := func(src, want string) func(t *testing.T) {
		return func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("std.raw", opt{std: true, noPrelude: true}, raw)
			checkBody(t, w, want, src)
		}
	}
	t.Run("raw.Read outside unsafe", rawCase("import std.raw\n\nF(x: &int): int {\n\treturn raw.Read(raw.Of(x))\n}\n", "E1101@4:9"))
	t.Run("raw.Of is safe", rawCase("import std.raw\n\nF(x: &int): *int {\n\treturn raw.Of(x)\n}\n", ""))
	t.Run("raw.Str in unsafe", rawCase("import std.raw\n\nF(p: *u8): str {\n\tunsafe \"p holds 3 bytes\" {\n\t\treturn raw.Str(p, 3)\n\t}\n}\n", ""))
	runBodyCases(t, []bodyCase{
		{"packed(2) i64 field", "packed(2) struct P {\n\ta: u8\n\tb: i64\n}\n\nF(p: &P): &i64 {\n\treturn &p.b\n}\n", "E1101@7:9"},
		{"packed(2) u16 field", "packed(2) struct P {\n\ta: u8\n\tb: u16\n}\n\nF(p: &P): &u16 {\n\treturn &p.b\n}\n", ""},
		{"packed in unsafe", "packed struct P {\n\ta: u8\n\tb: i64\n}\n\nF(p: mut P) {\n\tunsafe \"aligned here\" {\n\t\tq := mut p.b\n\t}\n}\n", ""},
		{"packed mut", "packed struct P {\n\ta: u8\n\tb: i64\n}\n\nF(p: mut P) {\n\tq := mut p.b\n}\n", "E1101@7:7"},
	})
}

// TestE1102_TwoSitesOneModule is check's half of E1102: in a module that is
// not unsafe-permitted, check reports no E1102 (the driver's, in phase
// imports) and checks the blocks' contents as if permitted.
func TestE1102_TwoSitesOneModule(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"two blocks", "F(p: *int): int {\n\tunsafe \"one\" {\n\t\tx := *p\n\t}\n\tunsafe \"two\" {\n\t\treturn *p\n\t}\n}\n", ""},
		{"contents checked", "F(p: *int) {\n\tunsafe \"one\" {\n\t\tx: u8 = *p\n\t}\n}\n", "E0301@3:11"},
	})
}
