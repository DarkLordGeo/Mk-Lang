// Why: restated clauses, EvalArena, instance flows, impl arguments and the
// empty str const each had a gap between a cold build, a warm build and
// codegen.
// What: TestLoadReadsModuleImports, TestRestatedClauseFromLoad, TestSubstClause, TestEvalArenaIsRef, TestIncrSliceNoImplRead,
// TestGenericInstanceFnFlows, TestImplArgs, TestEmptyStrConstIsNonNull.
// How: checks small programs, some through Load, and compares codes, flows
// and const values.

package check

import (
	"bytes"
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"
)

// TestRestatedClauseFromLoad: Contract.Text. A generic caller
// that passes its const parameter on to lib.F[N] (requires N > 0) without
// restating `M > 0` is E0706 with the clause, against a callee checked from
// source and against one Load decoded (no AST). Before, the Load case read
// the predicate's AST, found none and skipped the rule: a warm build
// accepted what a cold one rejected. The restated caller stays clean both
// ways.
func TestRestatedClauseFromLoad(t *testing.T) {
	w, lib := loadWorld(t)
	bad := w.add("bad", opt{stopBefore: true}, "import lib\n\nH[const M: int](): int {\n\treturn lib.F[M]()\n}\n")
	good := w.add("good", opt{stopBefore: true}, "import lib\n\nH[const M: int](): int\n\trequires M > 0 {\n\treturn lib.F[M]()\n}\n")
	c2, _ := loadedChecker(t, w, w.core, lib.id)
	for _, x := range []struct {
		name string
		c    *Checker
	}{{"Signatures", w.c}, {"Load", c2}} {
		got := checkApp(t, w, x.c, bad)
		if !bytes.Contains(got.json, []byte(`"E0706"`)) || !bytes.Contains(got.json, []byte(`"clause":"M > 0"`)) {
			t.Errorf("%s: want E0706 with clause M > 0: %s", x.name, got.json)
		}
		if g := checkApp(t, w, x.c, good); bytes.Contains(g.json, []byte(`"E0706"`)) {
			t.Errorf("%s: restated caller: %s", x.name, g.json)
		}
	}
}

// TestSubstClause: the text substitution the restated rule now runs
// over Contract.Text replaces names, not fields, numbers or literal text.
func TestSubstClause(t *testing.T) {
	w := newWorld(t, true)
	n := w.in.Intern("N")
	sub := map[source.Symbol]string{n: "M", w.in.Intern("x"): "7", w.in.Intern("r"): "R2", w.in.Intern("b"): "B2"}
	for _, c := range []struct{ in, want string }{
		{"N > 0", "M > 0"},
		{"N + N1 < Len(N)", "M + N1 < Len(M)"},
		{"a.N == N", "a.N == M"},
		{`F("N", 'N') == N`, `F("N", 'N') == M`},
		{`G("a\"N") > x`, `G("a\"N") > 7`},
		{"0x1N == N", "0x1N == M"},
		// A raw string takes no escape, and a prefix is
		// no name.
		{`F(r"\") == N`, `F(r"\") == M`},
		{`F(r"C:\", r) == N`, `F(r"C:\", R2) == M`},
		{`G(b"\"N") == N`, `G(b"\"N") == M`},
		{`G(b'N') + r + b == N`, `G(b'N') + R2 + B2 == M`},
		{`xr"N" == N`, `xr"N" == M`},
		// A quote inside a multi-line literal does not end it.
		{"F(\"\"\"\n    say \"N\"\n    \"\"\") == N", "F(\"\"\"\n    say \"N\"\n    \"\"\") == M"},
		{"F(\"\"\"\n    a\\\"\"\"N\n    \"\"\") == N", "F(\"\"\"\n    a\\\"\"\"N\n    \"\"\") == M"},
	} {
		if got := w.c.substClause(c.in, sub); got != c.want {
			t.Errorf("substClause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEvalArenaIsRef: EvalArena() is a &Arena,
// so a by-value Arena parameter is E0301 and a &Arena one is
// clean.
func TestEvalArenaIsRef(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"by reference", "Build(a: &Arena): int {\n\treturn 1\n}\nconst K: int = Build(EvalArena())\n", ""},
		{"by value", "Build(a: Arena): int {\n\treturn 1\n}\nconst K: int = Build(EvalArena())\n", "E0301@4:22"},
	})
}

// TestIncrSliceNoImplRead: sliceElemOf records ReadNoImpl(Slice, v) when
// v's constructor has no Slice impl, so adding one rechecks a unit that
// could not index or iterate v (the driver's implHash needs it), and
// ReadImpl when it has one.
func TestIncrSliceNoImplRead(t *testing.T) {
	const lib = "export struct Q {\n\texport b: []u8\n}\n\nexport struct B {\n\texport b: []u8\n}\n\nimpl Slice[u8] for B {\n}\n"
	for _, tc := range []struct {
		name, src string
		kind      types.ReadKind
	}{
		{"no impl", "import lib\n\nF(q: &lib.Q): u8 {\n\treturn q[0]\n}\n", types.ReadNoImpl},
		{"impl", "import lib\n\nF(q: &lib.B): u8 {\n\treturn q[0]\n}\n", types.ReadImpl},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := false
			var all []string
			for _, r := range bodyReads(t, lib, tc.src) {
				all = append(all, r.Kind.String()+" "+r.Key.String()+" "+r.Name)
				if r.Kind == tc.kind && strings.HasSuffix(r.Key.String(), ".Slice") {
					found = true
				}
			}
			if !found {
				t.Errorf("no %s of Slice in %v", tc.kind, all)
			}
		})
	}
}

// TestGenericInstanceFnFlows: a generic function instance as a fn value
// keeps only the flows its type arguments leave meaningful (instFlows).
// `Id[T](x: T): T` at int has no flow, so it is a
// `fn(int): int` (before, `from #1` made each of these E0608, and no fn
// type over int could declare it); at &int it keeps `from #1`, which a
// target's `from y` does not cover.
func TestGenericInstanceFnFlows(t *testing.T) {
	const id = "Id[T](x: T): T {\n\treturn x\n}\n\nPk[T](a: T, b: T): T from a {\n\treturn a\n}\n\n"
	runBodyCases(t, []bodyCase{
		// A generic named as a value takes its type arguments,
		// so the rows write them; each still tests an instance's flows.
		{"written binding", id + "F(): int {\n\tg: fn(x: int): int = Id[int]\n\treturn g(1)\n}\n", ""},
		{"written arguments", id + "F(): int {\n\tg: fn(x: int): int = Id[int]\n\treturn g(1)\n}\n", ""},
		{"argument", id + "Ap(f: fn(int): int): int {\n\treturn f(1)\n}\n\nF(): int {\n\treturn Ap(Id[int])\n}\n", ""},
		{"vacuous from on both", id + "F(): int {\n\tg: fn(x: int): int from x = Id[int]\n\treturn g(1)\n}\n", ""},
		{"reference keeps from", id + "F(a: &int): &int {\n\tg: fn(x: &int): &int from x = Id[&int]\n\treturn g(a)\n}\n", ""},
		{"two ints", id + "F(): int {\n\tg: fn(x: int, y: int): int = Pk[int]\n\treturn g(1, 2)\n}\n", ""},
		{"two refs, declared", id + "F(a: &int, b: &int): &int from a {\n\tg: fn(x: &int, y: &int): &int from x = Pk[&int]\n\treturn g(a, b)\n}\n", ""},
		{"two refs, other source", id + "F(a: &int, b: &int): &int from b {\n\tg: fn(x: &int, y: &int): &int from y = Pk[&int]\n\treturn g(a, b)\n}\n", "E0608@10:41"},
		// The old inferred forms (E0711 at the name).
		{"inferred binding", id + "F(): int {\n\tg: fn(x: int): int = Id\n\treturn g(1)\n}\n", "E0711@10:23"},
		{"inferred argument", id + "Ap(f: fn(int): int): int {\n\treturn f(1)\n}\n\nF(): int {\n\treturn Ap(Id)\n}\n", "E0711@14:12"},
		{"inferred two refs", id + "F(a: &int, b: &int): &int from a {\n\tg: fn(x: &int, y: &int): &int from x = Pk\n\treturn g(a, b)\n}\n", "E0711@10:41"},
	})
}

// TestImplArgs: the exported binding of an impl's parameters against a
// closed type (monoEnv.ImplMethod keeps a copy of match).
// Box[int] binds A to int; a type the target does not match, and an impl
// whose parameter the target leaves unbound, are refused.
func TestImplArgs(t *testing.T) {
	w := newWorld(t, true)
	b := w.add("m", opt{}, `interface Get {
	Val(&self): int
}

struct Box[T] {
	v: T
}

struct Plain2 {
	x: int
}

impl[A] Get for Box[A] {
	Val(&self): int {
		return 1
	}
}
`)
	impl := b.item("#impl(m.Get for m.Box)")
	box := b.item("Box")
	boxInt := w.tab.Named(box, []types.GenericArg{types.TypeArg(types.Int)})
	args, ok := w.c.ImplArgs(impl, boxInt)
	if !ok || len(args) != 1 || args[0].IsConst || args[0].Type != types.Int {
		t.Fatalf("ImplArgs(Box[int]) = %v, %v; want [int], true", args, ok)
	}
	if _, ok := w.c.ImplArgs(impl, w.tab.Named(b.item("Plain2"), nil)); ok {
		t.Error("ImplArgs(Plain2): want false")
	}
	if _, ok := w.c.ImplArgs(box, boxInt); ok {
		t.Error("ImplArgs of a struct: want false")
	}
}

// TestEmptyStrConstIsNonNull pins the canonical empty str and []u8 const:
// a zero length word and a data word that relocates
// to the interned zero-byte [0]u8 const, never a zero word with no reloc.
// The old zero word became a null data pointer under codegen's !nonnull,
// so LLVM took every path that reached the value as dead:
// `sys.Env(absent).Or("")` lost the exit test of System.Env's inlined loop
// in release, and a Windows --release build of the same shape with the old
// form faulted ("invalid memory access") where the tree prints its count.
func TestEmptyStrConstIsNonNull(t *testing.T) {
	w := newWorld(t, true)
	slice := w.tab.Slice(types.U8)
	for _, tc := range []struct {
		name string
		id   types.ConstID
	}{
		{"str", w.c.strConst(nil)},
		{"str from an empty slice", w.c.strConst([]byte{})},
		{"[]u8", w.c.bytesConst(slice, nil)},
	} {
		v := w.c.cfg.Consts.Get(tc.id)
		if len(v.Bytes) != 16 || types.LoadUint(v.Bytes[8:], 8) != 0 || types.LoadUint(v.Bytes[:8], 8) != 0 {
			t.Errorf("%s: bytes %x, want 16 zero bytes", tc.name, v.Bytes)
			continue
		}
		if len(v.Relocs) != 1 || v.Relocs[0].Offset != 0 || v.Relocs[0].Kind != types.RelocData {
			t.Errorf("%s: relocs %+v, want one RelocData at offset 0", tc.name, v.Relocs)
			continue
		}
		d := w.c.cfg.Consts.Get(v.Relocs[0].Data)
		if dt := w.tab.Get(d.Type); dt.Kind != types.KArray || dt.Elem != types.U8 || len(d.Bytes) != 0 {
			t.Errorf("%s: pointee kind %v elem %v with %d bytes, want [0]u8 of no bytes", tc.name, dt.Kind, dt.Elem, len(d.Bytes))
		}
	}
	if bs, ok := w.c.strBytes(w.c.strConst(nil)); !ok || len(bs) != 0 {
		t.Fatalf("strBytes of \"\": %q %v", bs, ok)
	}
}
