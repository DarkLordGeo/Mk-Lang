// Why: an error whose fields all have Format gets a provided Format, and
// the provided impl must look like a written one to every later stage.
// What: TestProvided, TestDroppedItemUnreachable,
// TestNestedErrorOrder, TestErrorSetFormat, TestE0704,
// TestFieldNamed.
// How: checks errors and error sets and reads the impl table, the method's
// plan and the read sets.

package check

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestProvided: an error whose fields have Format gets the impl, in the
// impl table, with the interface's signature, and its method is a unit
// whose plan holds the format's pieces; the reads of the fields' Format are
// on the error.
func TestProvided(t *testing.T) {
	t.Parallel()
	w, b, ds := checkNoICEWith(t, opt{}, "error NotFound(id: int, s: int)\nerror Denied\n")
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	impl := b.item("#impl(std.core.Format for m.NotFound)")
	meth := b.item("#impl(std.core.Format for m.NotFound):Format")
	d, ok := w.items.Get(impl).Decl.(*types.ImplDecl)
	if !ok || d.Bound.Item != w.c.known.Format || !slices.Equal(d.Methods, []types.ItemID{meth}) {
		t.Fatalf("impl decl %+v", w.items.Get(impl).Decl)
	}
	ck, _ := w.c.ctorOf(d.For)
	if b.m.implTab[implKey{iface: w.c.known.Format, ctor: ck}] != impl {
		t.Error("the provided impl is not in the impl table")
	}
	fd := w.items.Get(meth).Decl.(*types.FuncDecl)
	if got := w.c.sigString(&fd.Sig); !strings.Contains(got, "Format(&self, w: mut Writer): WriteError!void") {
		t.Errorf("signature %s", got)
	}
	if !slices.Contains(b.m.Units(), meth) || slices.Contains(b.m.Units(), impl) {
		t.Errorf("units %v: want the method, not the impl", b.m.Units())
	}
	bodies := b.unitBodies(t)
	plan := bodies["Format"]
	if plan == nil {
		t.Fatal("no Format unit")
	}
	var plans []*ErrorFormat
	for _, id := range b.m.Units() {
		if bd := w.c.Check(b.m, id); bd.ErrorFormat != nil {
			plans = append(plans, bd.ErrorFormat)
		}
	}
	if len(plans) != 2 {
		t.Fatalf("%d plans, want 2", len(plans))
	}
	var got []string
	for _, p := range plans {
		got = append(got, strings.Join(p.Pieces, "|"))
		for _, f := range p.Fields {
			if f.Method == types.NoItem {
				t.Errorf("field %d has no Format method", f.Index)
			}
		}
	}
	slices.Sort(got)
	if want := []string{"Denied", "NotFound(id: |, s: |)"}; !slices.Equal(got, want) {
		t.Errorf("pieces %q, want %q", got, want)
	}
	found := false
	for _, r := range b.m.Reads {
		if r.Item == b.item(".NotFound") && strings.Contains(readsString(r.Reads), "Format") {
			found = true
		}
	}
	if !found {
		t.Error("no Format read on the error (EF-8)")
	}
}

func readsString(rs types.ReadSet) string {
	var b strings.Builder
	for _, r := range rs.Sorted() {
		fmt.Fprintf(&b, "%s %s|", r.Key, r.Name)
	}
	return b.String()
}

// TestDroppedItemUnreachable: an error with a field lacking Format has
// no impl: its two items are in no Module.Items, Info.Items, impl table or
// unit, and printing it is E0706.
func TestDroppedItemUnreachable(t *testing.T) {
	t.Parallel()
	w, b, ds := checkNoICEWith(t, opt{}, "struct P {\n\tx: int\n}\nerror Bad(p: P)\nShow(w: mut Writer, e: Bad): WriteError!void {\n\ttry Print(w, \"{e}\", (e,))\n}\n")
	if len(ds) != 1 || ds[0].Code != diag.E0706 {
		t.Fatalf("diagnostics %v, want one E0706", ds)
	}
	key := types.KeyFromString("m#impl(std.core.Format for m.Bad)")
	impl, ok := w.items.ByKey(key)
	if !ok {
		t.Fatal("resolve declared no impl")
	}
	meth, _ := w.items.ByKey(types.ImplMemberKey(key, "Format"))
	for _, id := range []types.ItemID{impl, meth} {
		if slices.Contains(w.items.Module(b.id).Items, id) || slices.Contains(b.m.Units(), id) {
			t.Errorf("dropped item %d still listed", id)
		}
		for _, ii := range b.info.Items {
			if ii.Item == id {
				t.Errorf("dropped item %d in Info.Items", id)
			}
		}
		for _, v := range b.m.implTab {
			if v == id {
				t.Errorf("dropped item %d in the impl table", id)
			}
		}
	}
	if b.m.errFmtGap[b.item(".Bad")] != 0 {
		t.Error("no gap recorded")
	}
}

// TestE0704: a written impl Format for an error is E0704 at `impl`,
// reason compiler-provided, with and without a provided impl, and nothing
// inside it is checked.
func TestE0704(t *testing.T) {
	t.Parallel()
	const written = "impl Format for %s {\n\tFormat(&self, w: mut Writer): WriteError!void {\n\t\ttry w.Str(Undefined)\n\t}\n}\n"
	for _, c := range []struct{ name, src, alt string }{
		{"provided", "error Denied\n" + strings.Replace(written, "%s", "Denied", 1), ""},
		{"dropped", "struct P {\n\tx: int\n}\nerror Bad(p: P)\n" + strings.Replace(written, "%s", "Bad", 1), "give the field `p` a `Format`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, _, ds := checkNoICEWith(t, opt{}, c.src)
			if len(ds) != 1 || ds[0].Code != diag.E0704 {
				t.Fatalf("diagnostics %v, want one E0704", ds)
			}
			d := ds[0]
			if fact(d, "reason") != "compiler-provided" || fact(d, "interface") != "Format" {
				t.Errorf("facts %v", d.Facts)
			}
			if lc := w.lc(d.At); !strings.HasSuffix(lc, ":1") {
				t.Errorf("at %s, want the impl keyword", lc)
			}
			if c.alt != "" && !slices.Contains(d.Alternatives, c.alt) {
				t.Errorf("alternatives %v, want %q", d.Alternatives, c.alt)
			}
		})
	}
}

// TestErrorSetFormat: an error set formats when every member does
// (a Print hole and a Format bound); one member without Format refuses it.
func TestErrorSetFormat(t *testing.T) {
	t.Parallel()
	_, _, ds := checkNoICEWith(t, opt{}, "error A(n: int)\nerror B\ntype S = A | B\nShow[T: Format](w: mut Writer, x: T): WriteError!void {\n\ttry x.Format(w)\n}\nF(w: mut Writer, e: S): WriteError!void {\n\ttry Print(w, \"{e}\", (e,))\n\ttry Show(w, e)\n}\n")
	if len(ds) != 0 {
		t.Fatalf("diagnostics %v", ds)
	}
	_, _, ds = checkNoICEWith(t, opt{}, "struct P {\n\tx: int\n}\nerror A(p: P)\nerror B\ntype S = A | B\nF(w: mut Writer, e: S): WriteError!void {\n\ttry Print(w, \"{e}\", (e,))\n}\n")
	if len(ds) != 1 || ds[0].Code != diag.E0706 {
		t.Fatalf("diagnostics %v, want one E0706", ds)
	}
}

// TestFieldNamed: printing an error whose provided Format was dropped
// names the field (E0706 at a hole, E0701 at a Format bound).
func TestFieldNamed(t *testing.T) {
	t.Parallel()
	const pre = "struct P {\n\tx: int\n}\nerror Bad(pos: P)\nerror Ok\ntype S = Bad | Ok\nShow[T: Format](w: mut Writer, x: T): WriteError!void {\n\ttry x.Format(w)\n}\n"
	for _, c := range []struct {
		name, body string
		code       diag.Code
	}{
		{"hole", "F(w: mut Writer, e: Bad): WriteError!void {\n\ttry Print(w, \"{e}\", (e,))\n}\n", diag.E0706},
		{"set hole", "F(w: mut Writer, e: S): WriteError!void {\n\ttry Print(w, \"{e}\", (e,))\n}\n", diag.E0706},
		{"bound", "F(w: mut Writer, e: Bad): WriteError!void {\n\ttry Show(w, e)\n}\n", diag.E0701},
		{"method", "F(w: mut Writer, e: Bad): WriteError!void {\n\ttry e.Format(w)\n}\n", diag.E0702},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, ds := checkNoICEWith(t, opt{}, pre+c.body)
			if len(ds) != 1 || ds[0].Code != c.code {
				t.Fatalf("diagnostics %v %q, want one %s", ds[0].Code, ds[0].Message, c.code)
			}
			if fact(ds[0], "field") != "pos" || fact(ds[0], "field_type") != "P" || !strings.Contains(ds[0].Message, "`Bad` has no `Format`") {
				t.Errorf("message %q, facts %v", ds[0].Message, ds[0].Facts)
			}
		})
	}
}

// TestNestedErrorOrder: an error whose field holds another error of the
// module under a generic constructor is decided after that error (its
// dependencies first), whatever the key order.
func TestNestedErrorOrder(t *testing.T) {
	t.Parallel()
	_, _, ds := checkNoICEWith(t, opt{}, "struct W[T] {\n\tv: T\n}\nimpl[T: Format] Format for W[T] {\n\tFormat(&self, w: mut Writer): WriteError!void {\n\t}\n}\nerror A(x: W[Z])\nerror Z\nF(w: mut Writer, a: A): WriteError!void {\n\ttry Print(w, \"{a}\", (a,))\n}\n")
	if len(ds) != 0 {
		t.Fatalf("diagnostics %v", ds)
	}
}
