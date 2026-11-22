// Why: E0604 must name what a reader cannot see: the field of a `Ctx`
// through which a store reaches state, with a rewrite that checks when
// applied as written, and the bound a parameter's walk stopped at when a
// referent past it is what takes the store.
// What: TestCtxStateRewrites, TestCtxStateNoNote and
// TestStoreNamesReferentBound.
// How: each `Ctx` shape is checked, its try line read, and the rewrite it
// names applied and checked clean; shapes that are no `Ctx` get no note; a
// static store judged through a referent past the bound names the bound,
// and stores the bound does not decide name none.

package driver

import (
	"fmt"
	"strings"
	"testing"
)

// ctxAlt is the one E0604 of src, with its `field` fact and the try line
// that names the `Ctx` ("" when there is none).
func ctxAlt(t *testing.T, src string) (field, alt string) {
	t.Helper()
	r := checkText(t, src)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code.String() != "E0604" {
		t.Fatalf("want one E0604:\n%s", diagLines(r))
	}
	d := r.Report.Diags[0].Diag
	for _, a := range d.Alternatives {
		if strings.Contains(a, "Ctx") {
			alt = a
		}
	}
	return fact(d, "field"), alt
}

// TestCtxStateRewrites: for each shape the E0604 names `c.st` and a clause,
// and the rewrite with `st` passed beside the `Ctx` under exactly that
// clause checks clean: a value allocated with the Ctx's allocator stored
// into the state (`*c into st`, and the caller then takes the state apart),
// another parameter's loan (`v: &i32 into st`), the state's own reference
// stored into it and a reference read from inside it (`st: &Shared into
// st`).
func TestCtxStateRewrites(t *testing.T) {
	t.Parallel()
	const mapShared = "struct Shared {\n    m: Mutex[TextMap[HeapText]]\n}\n\n"
	const putBody = "    var g := try ST.m.Lock(c.io)\n    defer g.Unlock()\n    var v := HeapText.New(c.a)\n    onerr v.Free(c.a)\n    try v.Push(c.a, value)\n    try g.Mut().Set(c.a, key, v)\n}\n"
	const cellShared = "struct Shared {\n    root: &i32\n    cell: Cell[?&i32]\n    me: Cell[?&Shared]\n}\n\n"
	const ctx = "struct Ctx {\n    st: &Shared\n    a: &Alloc\n}\n\n"
	const ctxRw = "struct Ctx {\n    a: &Alloc\n}\n\n"
	cases := []struct {
		name, src, clause, rewrite string
	}{
		{"allocator", mapShared + "struct Ctx {\n    st: &Shared\n    a: &Alloc\n    io: Io\n}\n\n" +
			"Put(c: &Ctx, key: str, value: str): Canceled|OutOfMemory!void {\n" + strings.ReplaceAll(putBody, "ST", "c.st"),
			"*c into st",
			mapShared + "struct Ctx {\n    a: &Alloc\n    io: Io\n}\n\n" +
				"Put(c: &Ctx, st: &Shared, key: str, value: str): Canceled|OutOfMemory!void *c into st {\n" + strings.ReplaceAll(putBody, "ST", "st") +
				"\nexport Main(sys: &System) {\n    a := sys.heap.Alloc()\n    st := Shared(m: Mutex.New(TextMap[HeapText].New(a)))\n    c := Ctx(a: a, io: sys.io)\n    Put(&c, &st, \"k\", \"v\") else { }\n    Shared(m) := st\n    m.Into().Free(a)\n}\n"},
		{"parameter", cellShared + ctx + "Put(c: &Ctx, v: &i32) {\n    c.st.cell.Store(some(v))\n}\n",
			"v: &i32 into st",
			cellShared + ctxRw + "Put(c: &Ctx, st: &Shared, v: &i32 into st) {\n    st.cell.Store(some(v))\n}\n"},
		{"itself", cellShared + ctx + "Put(c: &Ctx) {\n    c.st.me.Store(some(c.st))\n}\n",
			"st: &Shared into st",
			cellShared + ctxRw + "Put(c: &Ctx, st: &Shared into st) {\n    st.me.Store(some(st))\n}\n"},
		{"inside", cellShared + ctx + "Put(c: &Ctx) {\n    c.st.cell.Store(some(c.st.root))\n}\n",
			"st: &Shared into st",
			cellShared + ctxRw + "Put(c: &Ctx, st: &Shared into st) {\n    st.cell.Store(some(st.root))\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			field, alt := ctxAlt(t, c.src)
			if field != "c.st" || !strings.Contains(alt, "codebase.md section 3") || !strings.HasSuffix(alt, "write `"+c.clause+"`") {
				t.Fatalf("field %q, try line %q; want c.st and `%s`", field, alt, c.clause)
			}
			if !strings.Contains(c.rewrite, c.clause) {
				t.Fatalf("the rewrite does not carry the clause %q", c.clause)
			}
			if r := checkText(t, c.rewrite); len(r.Report.Diags) != 0 {
				t.Errorf("the rewrite: want clean:\n%s", diagLines(r))
			}
		})
	}
}

// TestCtxStateNoNote: shapes that are no `Ctx` get no field and no Ctx try
// line: two references to state, a method's receiver, and a linked node
// that holds no capability.
func TestCtxStateNoNote(t *testing.T) {
	t.Parallel()
	const shared = "struct Shared {\n    cell: Cell[?&i32]\n}\n\n"
	for name, src := range map[string]string{
		"two":      shared + "struct Ctx {\n    st: &Shared\n    other: &Shared\n    a: &Alloc\n}\n\nPut(c: &Ctx, v: &i32) {\n    c.st.cell.Store(some(v))\n}\n",
		"receiver": shared + "struct Cursor {\n    buf: &Shared\n    a: &Alloc\n}\n\nCursor.Put(&self, v: &i32) {\n    self.buf.cell.Store(some(v))\n}\n",
		"node":     "struct N {\n    cell: Cell[?&i32]\n    next: ?&N\n}\n\nPut(n: &N, v: &i32) {\n    m := n.next else {\n        return\n    }\n    m.cell.Store(some(v))\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if field, alt := ctxAlt(t, src); field != "" || alt != "" {
				t.Errorf("field %q, try line %q; want none", field, alt)
			}
		})
	}
}

// TestStoreNamesReferentBound: a static stored into the one referent that
// takes stores, the 65th instance of B and so past the walk's stop, is
// E0604 with the fact `bound`; the same store with 10 instances, a store at
// the first hop of a polymorphically recursive G whose every instance takes
// stores, and a store a clause clears name no bound.
func TestStoreNamesReferentBound(t *testing.T) {
	t.Parallel()
	const gm = "var GM: Mutex[?&i32] = Mutex.New(none)\n\n"
	wide := func(n int) string {
		var b strings.Builder
		b.WriteString("struct B[T] {\n    v: T\n}\n\n" + gm + "struct Wide {\n")
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, "    g%d: ?&B[[%d]u8]\n", i, i)
		}
		b.WriteString("    last: ?&B[Cell[?&Mutex[?&i32]]]\n}\n\nPut(w: &Wide) {\n    l := w.last else {\n        return\n    }\n    l.v.Store(some(&GM))\n}\n")
		return b.String()
	}
	const g = "struct G[T] {\n    v: T\n    c: Cell[?&Mutex[?&i32]]\n    next: ?&G[?T]\n}\n\n"
	for _, c := range []struct{ name, src, bound string }{
		{"past", wide(64), "more than 64 instances of `B`"},
		{"under", wide(10), ""},
		{"firsthop", g + gm + "Put(g: &G[int]) {\n    n := g.next else {\n        return\n    }\n    n.c.Store(some(&GM))\n}\n", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := checkText(t, c.src)
			if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code.String() != "E0604" {
				t.Fatalf("want one E0604:\n%s", diagLines(r))
			}
			if got := fact(r.Report.Diags[0].Diag, "bound"); got != c.bound {
				t.Errorf("bound %q, want %q", got, c.bound)
			}
		})
	}
	src := "struct G[T] {\n    v: T\n    c: Cell[?&i32]\n    next: ?&G[?T]\n}\n\nPut(g: &G[int], x: &i32) {\n    n := g.next else {\n        return\n    }\n    n.c.Store(some(x))\n}\n"
	r := checkText(t, src)
	for _, e := range r.Report.Diags {
		if b := fact(e.Diag, "bound"); b != "" {
			t.Errorf("clause store: bound %q, want none", b)
		}
	}
}
