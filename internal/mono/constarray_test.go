// Why: an array or struct literal passed for a const value parameter, as in
// `Pick(&xs, [1, 3])`, once hit "const parameter argument is not a constant
// (operand kind 0)", an ICE in collect's instance-limit walk.
// What: TestConstArrayLiteralArg and TestConstAggregateWithStrArg.
// How: collects programs that pass such literals and checks the instance
// arguments and that no diagnostic or ICE appears.

package mono

import (
	"fmt"
	"testing"

	"mk/internal/types"
)

func TestConstArrayLiteralArg(t *testing.T) {
	src := `Pick(xs: &[4]int, const p: [2]int): int {
    return xs[p[0]] + xs[p[1]]
}

Grid(const g: [2][2]i32): i32 {
    return g[1][0]
}

Root(): int {
    xs: [4]int = [1, 2, 3, 4]
    return Pick(&xs, [1, 3]) + Pick(&xs, [0, 2]) + int(Grid([[1, 2], [3, 4]]))
}
`
	sw := newSrcWorld(t, src)
	lw := layoutWorld{sw}
	in, ds := Collect(lw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	var picks [][]int64
	grids := 0
	for i := range in.List {
		inst := &in.List[i]
		if len(inst.Args) != 1 || !inst.Args[0].IsConst {
			continue
		}
		v := sw.consts.Get(inst.Args[0].Const.Value)
		switch {
		case len(inst.Name) > 6 && inst.Name[:6] == "m.Pick":
			if len(v.Bytes) != 16 {
				t.Fatalf("%s: const of %d bytes, want 16", inst.Name, len(v.Bytes))
			}
			picks = append(picks, []int64{int64(types.LoadUint(v.Bytes[0:], 8)), int64(types.LoadUint(v.Bytes[8:], 8))})
		case len(inst.Name) > 6 && inst.Name[:6] == "m.Grid":
			grids++
			want := []byte{1, 0, 0, 0, 2, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0}
			if string(v.Bytes) != string(want) {
				t.Errorf("%s: bytes %v, want %v", inst.Name, v.Bytes, want)
			}
		}
	}
	if len(picks) != 2 || grids != 1 {
		t.Fatalf("Pick instances %v and %d Grid instances, want 2 and 1", picks, grids)
	}
	seen := map[[2]int64]bool{}
	for _, p := range picks {
		seen[[2]int64{p[0], p[1]}] = true
	}
	if !seen[[2]int64{1, 3}] || !seen[[2]int64{0, 2}] {
		t.Errorf("Pick instances %v, want [1 3] and [0 2]", picks)
	}
}

// TestConstAggregateWithStrArg: an array or struct literal of `str`
// parts passed for a const parameter is a constant: each part's pointer
// relocation moves to the part's offset, so the instance's argument equals the
// one a named const gives, and no E0000 is raised.
func TestConstAggregateWithStrArg(t *testing.T) {
	src := `struct Two {
    a: str
    b: str
}

PickS(const p: [2]str): int {
    return p[0].Len() + p[1].Len()
}

PickT(const p: Two): int {
    return p.a.Len() + p.b.Len()
}

Root(): int {
    return PickS(["ab", "cde"]) + PickT(Two(a: "x", b: "yz"))
}
`
	sw := newSrcWorld(t, src)
	lw := layoutWorld{sw}
	in, ds := Collect(lw, []Root{{Kind: RootExport, Item: sw.item(t, "Root")}}, Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	found := map[string][]string{}
	for i := range in.List {
		inst := &in.List[i]
		if len(inst.Args) != 1 || !inst.Args[0].IsConst {
			continue
		}
		v := sw.consts.Get(inst.Args[0].Const.Value)
		var parts []string
		for _, r := range v.Relocs {
			if r.Kind != types.RelocData {
				t.Fatalf("%s: reloc at %d is not a data reloc", inst.Name, r.Offset)
			}
			d := sw.consts.Get(r.Data)
			parts = append(parts, fmt.Sprintf("%d:%s", r.Offset, d.Bytes))
		}
		found[inst.Name[:7]] = parts
	}
	want := map[string]string{"m.PickS": "[0:ab 16:cde]", "m.PickT": "[0:x 16:yz]"}
	for name, w := range want {
		if got := fmt.Sprint(found[name]); got != w {
			t.Errorf("%s instance's const parts %s, want %s", name, got, w)
		}
	}
}
