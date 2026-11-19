// Why: codegen packs instances into object files, and an edit must rebuild
// only the packs it touches, or incremental builds lose their point.
// What: TestPartitionLocalEdit and TestPartitionStable over synth, a module
// of generated placeholder instances.
// How: partitions before and after growing one instance's body and counts
// the packs that changed (changed).

package mono

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/iface"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// synth is a module of n instances of ops operations each, with names and
// contents from a seed string; their bodies are placeholders.
func synth(w *world, mod types.ModuleID, seed string, n int, ops uint32) []Instance {
	var out []Instance
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s.F%04d", seed, i)
		out = append(out, Instance{Key: Key{Text: name, Hash: source.HashString("mono.test.name", name)},
			Owner: mod, Body: &mir.Body{}, Ops: ops, Content: source.HashString("mono.test.content", name)})
	}
	return out
}

func packText(p Pack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:", p.Name)
	for _, pi := range p.Instances {
		fmt.Fprintf(&b, " %x/%x", pi.Name[:4], pi.Content[:4])
	}
	return b.String()
}

func packSet(ps []Pack) map[string]bool {
	m := map[string]bool{}
	for _, p := range ps {
		m[packText(p)] = true
	}
	return m
}

// changed counts the packs of b that are not packs of a.
func changed(a, b []Pack) int {
	as := packSet(a)
	n := 0
	for _, p := range b {
		if !as[packText(p)] {
			n++
		}
	}
	return n
}

// TestPartitionLocalEdit: a module of 320 instances of 625 operations
// (200,000 in all) cuts into several packs; growing one instance's body (by
// 500 operations, a new content hash) changes exactly one pack; one added
// instance changes only the pack it lands in, split in two at most; an
// instance carried over PackSoloOps changes its pack and one neighbour.
func TestPartitionLocalEdit(t *testing.T) {
	w := newWorld()
	base := &Instances{List: synth(w, w.mod, "m", 320, 625)}
	p0 := Partition(w, base, Options{})
	if len(p0) < 4 {
		t.Fatalf("only %d packs", len(p0))
	}
	total := 0
	for _, p := range p0 {
		total += len(p.Members)
		if !strings.HasPrefix(p.Name, "m.") || len(p.Name) != len("m.")+16 {
			t.Fatalf("pack name %q", p.Name)
		}
	}
	if total != 320 {
		t.Fatalf("%d members", total)
	}

	edit := &Instances{List: append([]Instance(nil), base.List...)}
	edit.List[100].Ops += 500
	edit.List[100].Content = source.HashString("mono.test.content", "edited")
	p1 := Partition(w, edit, Options{})
	if len(p1) != len(p0) || changed(p0, p1) != 1 {
		t.Fatalf("body edit: %d packs (was %d), %d changed", len(p1), len(p0), changed(p0, p1))
	}

	add := &Instances{List: append(append([]Instance(nil), base.List...), synth(w, w.mod, "extra", 1, 625)...)}
	p2 := Partition(w, add, Options{})
	if c := changed(p0, p2); c < 1 || c > 2 || len(p2)-len(p0) > 1 {
		t.Fatalf("added instance: %d changed, %d packs (was %d)", c, len(p2), len(p0))
	}

	solo := &Instances{List: append([]Instance(nil), base.List...)}
	solo.List[200].Ops = iface.PackSoloOps + 1
	p3 := Partition(w, solo, Options{})
	if c := changed(p0, p3); c < 1 || c > 3 {
		t.Fatalf("solo instance: %d changed", c)
	}
	var alone bool
	for _, p := range p3 {
		if len(p.Members) == 1 && p.Members[0] == 200 {
			alone = true
		}
	}
	if !alone {
		t.Fatal("an instance over PackSoloOps is not a pack of its own")
	}
}

// TestPartitionStable: packs of one module do not change when another
// module's instances change; bodiless instances are in no pack; packs never
// cross modules.
func TestPartitionStable(t *testing.T) {
	w := newWorld()
	other := w.items.AddModule(&types.Module{Path: "n"})
	a := synth(w, w.mod, "m", 100, 100)
	b := synth(w, other, "n", 100, 100)
	ext := Instance{Key: Key{Text: "m.puts", Hash: source.HashString("mono.test.name", "puts")}, Owner: w.mod}
	in := &Instances{List: append(append(append([]Instance(nil), a...), b...), ext)}
	p0 := Partition(w, in, Options{})
	b2 := synth(w, other, "n2", 120, 300)
	in2 := &Instances{List: append(append([]Instance(nil), a...), b2...)}
	p1 := Partition(w, in2, Options{})
	keep := func(ps []Pack, l *Instances, owner types.ModuleID) []string {
		var out []string
		for _, p := range ps {
			if p.Owner == owner {
				out = append(out, packText(p))
			}
			for _, m := range p.Members {
				if l.List[m].Owner != p.Owner {
					t.Fatalf("pack %s crosses modules", p.Name)
				}
			}
		}
		return out
	}
	if x, y := keep(p0, in, w.mod), keep(p1, in2, w.mod); strings.Join(x, "\n") != strings.Join(y, "\n") {
		t.Fatalf("module m's packs changed:\n%s\nvs\n%s", strings.Join(x, "\n"), strings.Join(y, "\n"))
	}
	for _, p := range p0 {
		for _, m := range p.Members {
			if in.List[m].Body == nil {
				t.Fatal("a bodiless instance is in a pack")
			}
		}
	}
}
