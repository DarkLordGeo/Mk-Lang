// Why: a fields loop is unrolled at check, once per field, so its typing,
// determinism and cost all live here.
// What: TestFieldsLoop*, TestFieldsUnrollCostIsTheLoops and
// BenchmarkFieldsUnrollLargeUnit.
// How: checks loops over concrete and generic structs and compares codes,
// Body.Locals and the time a larger unit takes.

package check

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"mk/internal/types"
)

const dumpSrc = "Dump[A: Fields[Format] + Plain](var w: Writer, args: A): WriteError!void {\n\tfor name, value in fields(&args) {\n\t\ttry w.Str(name)\n\t\ttry value.Format(mut w)\n\t}\n}\n"

// TestFieldsLoop: the reference's Dump checks; fields(x) without & and a
// fields loop over an int are E0301 reason fields-of.
func TestFieldsLoop(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"dump", dumpSrc, ""},
		{"no reference", "F(p: (a: int)) {\n\tfor n, v in fields(p) {\n\t}\n}\n", "E0301@2:21"},
		{"not a struct", "F(p: int) {\n\tfor n, v in fields(&p) {\n\t}\n}\n", "E0301@2:22"},
		{"no bound", "G[A](a: A) {\n\tfor n, v in fields(&a) {\n\t}\n}\n", "E0703@2:2"},
	})
}

// TestFieldsLoop_GenericHiddenParam: inside the generic loop value is
// &F with F the unit's hidden generic bounded by Format alone: a Format
// call is UseIfaceMethod, any other operation E0703.
func TestFieldsLoop_GenericHiddenParam(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "E0703@5:17", strings.Replace(dumpSrc, "\t}\n}\n", "\t\tx := *value + 1\n\t}\n}\n", 1))
	body := w.c.Check(b.m, b.item("Dump"))
	if len(body.Hidden) != 1 || len(body.Hidden[0].Bounds) != 1 {
		t.Fatalf("Hidden %+v", body.Hidden)
	}
	iface := 0
	for _, u := range body.Uses {
		if u.Kind == UseIfaceMethod {
			iface++
		}
	}
	if iface == 0 {
		t.Fatal("value.Format(w) is not an interface method call")
	}
}

// TestFieldsLoop_ConcreteHeterogeneous: a loop over a concrete struct is
// unrolled into one Copy per field, each with the field's own types; the
// main tables hold NoType for the body; an error in one field's copy is
// reported once with fact field.
func TestFieldsLoop_ConcreteHeterogeneous(t *testing.T) {
	w := newWorld(t, true)
	src := "struct P {\n\tn: int\n\ts: str\n}\nF(var w: Writer, p: P): WriteError!void {\n\tfor name, value in fields(&p) {\n\t\ttry value.Format(mut w)\n\t\tx := *value + 1\n\t}\n}\n"
	b := checkBody(t, w, "E0301@8:17", src)
	ds := b.bodyDiags(t)
	if fact(ds[0], "field") != "s" {
		t.Fatalf("field fact %q", fact(ds[0], "field"))
	}
	body := w.c.Check(b.m, b.item("F"))
	if len(body.Unrolled) != 2 || body.Unrolled[0].Field != 0 || body.Unrolled[1].Field != 1 || body.Unrolled[0].Parent != -1 {
		t.Fatalf("Unrolled %+v", body.Unrolled)
	}
	var vt []string
	for _, c := range body.Unrolled {
		for i, id := range c.Bind {
			if id != 0 && w.in.Str(body.Locals[id].Name) == "value" {
				vt = append(vt, w.tab.TypeString(body.Locals[id].Type, nil))
				_ = i
			}
		}
	}
	if got := strings.Join(vt, " "); got != "&int &str" {
		t.Fatalf("value types %q", got)
	}
	// The Format calls resolve to different impls per copy.
	var items []types.ItemID
	for _, c := range body.Unrolled {
		for _, u := range c.Uses {
			if u.Kind == UseMethod {
				items = append(items, u.Item)
				break
			}
		}
	}
	if len(items) != 2 || items[0] == items[1] {
		t.Fatalf("Format impls per copy %v", items)
	}
}

// TestFieldsLoop_UnrollCap: past the per-unit node cap the loop is E0708
// reason fields-unroll and its body is not checked.
func TestFieldsLoop_UnrollCap(t *testing.T) {
	old := fieldsNodeCap
	fieldsNodeCap = 20
	defer func() { fieldsNodeCap = old }()
	w := newWorld(t, true)
	b := checkBody(t, w, "E0708@2:2", "F(p: (a: int, b: int, c: int)) {\n\tfor name, value in fields(&p) {\n\t\tx := *value + 1\n\t\ty := x * 2\n\t\t_ = y\n\t}\n}\n")
	if d := b.bodyDiags(t)[0]; fact(d, "reason") != "fields-unroll" || fact(d, "limit") != "20" {
		t.Fatalf("facts %s", dump(w, b.bodyDiags(t)))
	}
}

// fieldsLocals renders a body's locals (name and node) and each copy's
// bindings, for comparing runs.
func fieldsLocals(w *world, body *Body) string {
	var sb strings.Builder
	for _, l := range body.Locals {
		fmt.Fprintf(&sb, "%s@%d,", w.in.Str(l.Name), l.Node)
	}
	for _, c := range body.Unrolled {
		fmt.Fprintf(&sb, "|%d:%d:%v", c.Field, c.Parent, c.Bind)
	}
	return sb.String()
}

// TestFieldsLoopLocalsDeterministic: the fresh locals of a
// concrete fields loop's copies are appended in declaration order, so
// Body.Locals and every Copy's Bind are the same on every run, with
// GOMAXPROCS 1 and 8.
func TestFieldsLoopLocalsDeterministic(t *testing.T) {
	src := "struct P {\n\tx: int\n\ty: int\n}\n\nF(p: &P) {\n\tfor name, v in fields(&p) {\n\t\ta := 1\n\t\tb := 2\n\t\tc := 3\n\t\td := 4\n\t\t_ = a + b + c + d\n\t}\n}\n"
	var first string
	for _, procs := range []int{1, 8} {
		old := runtime.GOMAXPROCS(procs)
		w := newWorld(t, true)
		b := checkBody(t, w, "", src)
		for i := 0; i < 50; i++ {
			body := w.c.Check(b.m, b.item("F"))
			got := fieldsLocals(w, body)
			if first == "" {
				first = got
				// The fresh locals follow declaration order in each copy.
				var names []string
				for _, l := range body.Locals[len(body.Locals)-12:] {
					names = append(names, w.in.Str(l.Name))
				}
				if s := strings.Join(names, " "); s != "name v a b c d name v a b c d" {
					t.Fatalf("fresh locals %q", s)
				}
			} else if got != first {
				t.Fatalf("GOMAXPROCS %d run %d:\n%s\nwant\n%s", procs, i, got, first)
			}
		}
		runtime.GOMAXPROCS(old)
	}
}

// TestFieldsLoop_NestedLocals: in a fields loop nested in another, every use
// of a local declared in the inner body names the local its own copy binds
// (the inner copy's fresh local, not the outer copy's).
func TestFieldsLoop_NestedLocals(t *testing.T) {
	w := newWorld(t, true)
	src := "struct P {\n\tx: int\n\ty: int\n}\n\nF(p: &P) {\n\tfor n1, v1 in fields(&p) {\n\t\tfor n2, v2 in fields(&p) {\n\t\t\ta := 1\n\t\t\t_ = a\n\t\t}\n\t}\n}\n"
	b := checkBody(t, w, "", src)
	body := w.c.Check(b.m, b.item("F"))
	inner := 0
	for _, c := range body.Unrolled {
		if c.Parent < 0 {
			continue
		}
		inner++
		bound := map[LocalID]bool{}
		for _, id := range c.Bind {
			if id != 0 {
				bound[id] = true
			}
		}
		uses := 0
		for _, use := range c.Uses {
			if use.Kind == UseLocal && w.in.Str(body.Locals[use.Local].Name) == "a" {
				uses++
				if !bound[use.Local] {
					t.Errorf("copy %d/%d: a use of a names local %d, not one this copy binds %v", c.Parent, c.Field, use.Local, c.Bind)
				}
			}
		}
		if uses != 1 {
			t.Errorf("copy %d/%d: %d uses of a", c.Parent, c.Field, uses)
		}
	}
	if inner != 4 {
		t.Fatalf("%d inner copies, want 4", inner)
	}
}

// unrollSrc is a struct of nf int fields and one function of about pad
// padding statements holding one fields loop with a two-statement body.
func unrollSrc(nf, pad int) string {
	var sb strings.Builder
	sb.WriteString("struct P {\n")
	for i := 0; i < nf; i++ {
		fmt.Fprintf(&sb, "\tg%d: int\n", i)
	}
	sb.WriteString("}\n\nF(p: &P) {\n\tvar s := 0\n")
	for i := 0; i < pad; i++ {
		sb.WriteString("\ts = s + 1 * 2\n")
	}
	sb.WriteString("\tfor name, v in fields(&p) {\n\t\tx := *v\n\t\ts = s + x\n\t}\n}\n")
	return sb.String()
}

// unrollBytes is the bytes allocated by one Check of F in src.
func unrollBytes(t testing.TB, src string) int64 {
	w := newWorld(t, true)
	b := w.add("m", opt{}, src)
	id := b.item("F")
	w.c.Check(b.m, id)
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	w.c.Check(b.m, id)
	runtime.ReadMemStats(&m1)
	return int64(m1.TotalAlloc - m0.TotalAlloc)
}

// TestFieldsUnrollCostIsTheLoops: growing the unit around a fields
// loop over 200 fields costs what growing a unit with no loop costs, not
// that times the field count: copies are checked into scratch tables
// allocated once and copy out only the loop's node range.
func TestFieldsUnrollCostIsTheLoops(t *testing.T) {
	const nf = 200
	small, large := 200, 4000
	withLoop := unrollBytes(t, unrollSrc(nf, large)) - unrollBytes(t, unrollSrc(nf, small))
	plain := func(pad int) string {
		return strings.Replace(unrollSrc(nf, pad), "\tfor name, v in fields(&p) {\n\t\tx := *v\n\t\ts = s + x\n\t}\n", "", 1)
	}
	without := unrollBytes(t, plain(large)) - unrollBytes(t, plain(small))
	t.Logf("with loop %d, without %d", withLoop, without)
	// A unit-sized table per field would add nf times the growth.
	if withLoop > 3*without+1<<20 {
		t.Fatalf("growing the unit by %d statements costs %d bytes with the loop, %d without", large-small, withLoop, without)
	}
}

// BenchmarkFieldsUnrollLargeUnit: one Check of a unit of about
// 100k nodes holding one fields loop over 1,000 fields.
func BenchmarkFieldsUnrollLargeUnit(b *testing.B) {
	w := newWorld(b, true)
	m := w.add("m", opt{}, unrollSrc(1000, 14000))
	id := m.item("F")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.c.Check(m.m, id)
	}
}
