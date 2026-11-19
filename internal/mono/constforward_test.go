// Why: std.atomic passes const arguments in forms that once made collect
// raise "const parameter argument is not a constant", an ICE.
// What: TestConstFieldForwarded and TestPayloadFreeVariantConstArg:
// a field of a const value parameter forwarded to another const parameter
// (`o.v`), a struct literal of constants, and a payload-free enum variant
// (`E.C`) as const arguments.
// How: collects each form from source and checks the instance arguments.

package mono

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// layoutWorld is a srcWorld whose layouts are the checker's, so field
// offsets and enum tags are the driver's.
type layoutWorld struct{ *srcWorld }

func (lw layoutWorld) Layout(t types.TypeID) *types.Layout {
	l := lw.checker.Layout(t)
	return &l
}

// constCalls is the set of instance names that start with prefix.
func constCalls(t *testing.T, in *Instances, prefix string) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for i := range in.List {
		if strings.HasPrefix(in.List[i].Name, prefix) {
			got[in.List[i].Name] = true
		}
	}
	return got
}

func TestConstFieldForwarded(t *testing.T) {
	src := `struct Ord {
    v: u8
    w: i64
}

K(const k: u8): int {
    return int(k)
}

W(const k: i64): i64 {
    return k
}

O(const o: Ord): int {
    return K(o.v) + int(W(o.w))
}

Root(): int {
    return O(Ord(v: 1, w: 70)) + O(Ord(v: 2, w: -3))
}
`
	sw := newSrcWorld(t, src)
	in, ds := Collect(layoutWorld{sw}, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	ks := constCalls(t, in, "m.K[")
	if !ks["m.K[1]"] || !ks["m.K[2]"] || len(ks) != 2 {
		t.Errorf("K instances %v, want m.K[1] and m.K[2]", ks)
	}
	ws := constCalls(t, in, "m.W[")
	if !ws["m.W[70]"] || !ws["m.W[-3]"] || len(ws) != 2 {
		t.Errorf("W instances %v, want m.W[70] and m.W[-3]", ws)
	}
}

func TestPayloadFreeVariantConstArg(t *testing.T) {
	src := `enum E {
    A
    B(x: int)
    C
}

P(const e: E): int {
    match e {
        E.A { return 1 }
        E.B(x) { return x }
        E.C { return 3 }
    }
}

Root(): int {
    return P(E.C) + P(E.A)
}
`
	sw := newSrcWorld(t, src)
	lw := layoutWorld{sw}
	in, ds := Collect(lw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	var tags []uint64
	for i := range in.List {
		inst := &in.List[i]
		if !strings.HasPrefix(inst.Name, "m.P[") {
			continue
		}
		if len(inst.Args) != 1 || !inst.Args[0].IsConst {
			t.Fatalf("%s: args %+v, want one const", inst.Name, inst.Args)
		}
		v := sw.consts.Get(inst.Args[0].Const.Value)
		l := lw.Layout(v.Type)
		if !l.HasTag || uint64(len(v.Bytes)) != l.Shape.Size {
			t.Fatalf("%s: const of %d bytes, layout %+v", inst.Name, len(v.Bytes), l.Shape)
		}
		tags = append(tags, types.LoadUint(v.Bytes[l.Tag.Offset:], int(l.Tag.Size)))
	}
	l := lw.Layout(sw.tab.Named(sw.item(t, "E"), nil))
	want := map[uint64]bool{l.Variants[0].Discr: true, l.Variants[2].Discr: true}
	if len(tags) != 2 || !want[tags[0]] || !want[tags[1]] || tags[0] == tags[1] {
		t.Errorf("P instances have tags %v, want the discriminants of A and C %v", tags, want)
	}
}
