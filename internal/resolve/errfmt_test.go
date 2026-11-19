// Why: every error type gets a provided std.core.Format impl, and resolve
// must declare it so a written one is caught as a duplicate.
// What: TestSynthItems, TestNoCoreNoSynth,
// TestWrittenImplIsDuplicate.
// How: declares error types and checks the synthesized impl and method items
// (SynthErrorFormat), their nodes and keys.

package resolve

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/types"
)

func synthInfo(b *built, id types.ItemID) *ItemInfo {
	for _, ii := range b.info.Items {
		if ii.Item == id {
			return ii
		}
	}
	return nil
}

func TestSynthItems(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("m", opt{}, "error NotFound(id: i64)\nerror Denied\n")
	b.want(t)
	for _, name := range []string{"NotFound", "Denied"} {
		e := w.item("m." + name)
		impl := w.item("m#impl(std.core.Format for m." + name + ")")
		meth := w.item("m#impl(std.core.Format for m." + name + "):Format")
		if impl.Kind != types.KindImpl || impl.Owner != types.NoItem || impl.Span != e.Span {
			t.Errorf("%s impl: kind %v owner %d span %v, want impl, none, %v", name, impl.Kind, impl.Owner, impl.Span, e.Span)
		}
		if meth.Kind != types.KindFunc || meth.Owner != impl.ID || meth.Span != e.Span || w.in.Str(meth.Name) != "Format" {
			t.Errorf("%s method: kind %v owner %d span %v name %q", name, meth.Kind, meth.Owner, meth.Span, w.in.Str(meth.Name))
		}
		ei, ii, mi := synthInfo(b, e.ID), synthInfo(b, impl.ID), synthInfo(b, meth.ID)
		if ei == nil || ii == nil || mi == nil {
			t.Fatalf("%s: an item is missing from Info.Items", name)
		}
		if ei.Synth != SynthNone || ii.Synth != SynthErrorFormat || mi.Synth != SynthErrorFormat {
			t.Errorf("%s: Synth %v %v %v", name, ei.Synth, ii.Synth, mi.Synth)
		}
		if ii.Node != ei.Node || mi.Node != ei.Node || mi.Parent != impl.ID {
			t.Errorf("%s: nodes %d %d %d, method parent %d", name, ei.Node, ii.Node, mi.Node, mi.Parent)
		}
		// The error's own Ref still names the error.
		if r := b.info.Files[ei.File].Refs[ei.Node]; r.Kind != RefItem || types.ItemID(r.A) != e.ID {
			t.Errorf("%s: the error's Ref is %+v", name, r)
		}
	}
}

// TestNoCoreNoSynth: a module that sees no std.core gets no items.
func TestNoCoreNoSynth(t *testing.T) {
	t.Parallel()
	w := newWorld(t, false)
	b := w.add("m", opt{}, "error Denied\n")
	b.want(t)
	if _, ok := w.items.ByKey(types.KeyFromString("m#impl(std.core.Format for m.Denied)")); ok {
		t.Error("a synthesized impl in a module without std.core")
	}
}

// TestWrittenImplIsDuplicate: a written `impl Format for E` in E's
// module has the provided impl's key, so it is a DuplicateImpl naming the
// provided impl (check reports E0704); nothing inside it resolves.
func TestWrittenImplIsDuplicate(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := w.add("m", opt{}, `error Denied

impl Format for Denied {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("no")
    }
}
`)
	impl := w.item("m#impl(std.core.Format for m.Denied)")
	if len(b.info.DuplicateImpls) != 1 || b.info.DuplicateImpls[0].First != impl.ID {
		t.Fatalf("DuplicateImpls %+v, want one naming %d", b.info.DuplicateImpls, impl.ID)
	}
	if ii := synthInfo(b, impl.ID); ii == nil || ii.Synth != SynthErrorFormat {
		t.Fatal("the provided impl is not the declared one")
	}
	n := b.files[0].Items[1]
	if _, ok := n.(*ast.ImplDecl); !ok {
		t.Fatalf("item 1 is %T", n)
	}
}
