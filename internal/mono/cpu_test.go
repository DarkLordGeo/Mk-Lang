// Why: a generic called from a cpu "avx2" function and from baseline code
// needs two instances, or one of them runs the wrong instructions.
// What: TestFeatureKeyedInstances and TestTransitiveFeatures.
// How: collects cpuSrc and compares the key text of every instance, by name.

package mono

import (
	"slices"
	"strings"
	"testing"

	"mk/internal/types"
)

const cpuSrc = `Pick[T: Plain](x: T): T {
    return Inner(x)
}

Inner[T: Plain](x: T): T {
    return x
}

Plain1(x: i32): i32 {
    return x
}

Base(x: i32): i32 {
    return Pick(x)
}

Fast(x: i32): i32 cpu "avx2" {
    return Pick(x) +% Plain1(x)
}

Wider(x: i32): i32 cpu "avx2" {
    f := Pick[i32]
    return f(x)
}

Own[T: Plain](x: T): T cpu "avx2" {
    return x
}

Both(x: i32): i32 cpu "avx2" {
    return Own(x)
}

BaseOwn(x: i32): i32 {
    unsafe "the test calls it on an avx2 machine only" {
        return Own(x)
    }
}
`

// cpuKeys is the key text of every instance, by readable name.
func cpuKeys(in *Instances) map[string][]string {
	out := map[string][]string{}
	for i := range in.List {
		n := in.List[i].Name
		base, _, _ := strings.Cut(n, cpuOpen)
		out[base] = append(out[base], in.List[i].Key.Text)
	}
	return out
}

// TestFeatureKeyedInstances: a generic called from a cpu "avx2"
// function and from a baseline function has two instances, with different
// keys, hashes, symbols and pack names, the avx2 one carrying the inherited
// features; a plain callee, a fn value and a generic whose own clause
// already holds the caller's set have one.
func TestFeatureKeyedInstances(t *testing.T) {
	sw := newSrcWorld(t, cpuSrc)
	in, ds := Collect(sw, sw.limitRoots(), Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	keys := cpuKeys(in)
	pick := keys["m.Pick[i32]"]
	if len(pick) != 2 {
		t.Fatalf("Pick[i32] instances %q, want the baseline one and the avx2 one", pick)
	}
	var base, fast *Instance
	for i := range in.List {
		inst := &in.List[i]
		if !strings.HasPrefix(inst.Name, "m.Pick[i32]") {
			continue
		}
		if strings.Contains(inst.Key.Text, cpuOpen) {
			fast = inst
		} else {
			base = inst
		}
	}
	if base == nil || fast == nil {
		t.Fatalf("Pick[i32]: base %v, fast %v", base, fast)
	}
	if want := base.Key.Text + " @cpu(avx,avx2)"; fast.Key.Text != want {
		t.Errorf("avx2 key %q, want %q (the features beyond the baseline)", fast.Key.Text, want)
	}
	if !strings.HasSuffix(fast.Name, " @cpu(avx,avx2)") {
		t.Errorf("avx2 name %q lacks the suffix", fast.Name)
	}
	if len(base.CPU) != 0 || !slices.Contains(fast.CPU, "avx2") || !slices.Contains(fast.CPU, "avx") {
		t.Errorf("sets: base %q, avx2 %q", base.CPU, fast.CPU)
	}
	if base.Key.Hash == fast.Key.Hash || base.Symbol == fast.Symbol {
		t.Errorf("one hash or symbol for both: %s", base.Symbol)
	}
	if want := [32]byte(sw.Hasher().InstanceName(sw.Items().Get(base.Item).Key, base.Args)); base.Key.Hash != want {
		t.Errorf("the baseline instance's hash changed")
	}
	if want := [32]byte(sw.Hasher().InstanceNameCPU(sw.Items().Get(fast.Item).Key, fast.Args, []string{"avx", "avx2"})); fast.Key.Hash != want {
		t.Errorf("the avx2 instance's hash is not InstanceNameCPU over the inherited set")
	}
	names := map[[32]byte]bool{}
	for _, p := range Partition(sw, in, Options{}) {
		for _, pi := range p.Instances {
			if names[[32]byte(pi.Name)] {
				t.Errorf("two pack instances share a name")
			}
			names[[32]byte(pi.Name)] = true
		}
	}
	for _, one := range []string{"m.Plain1", "m.Own[i32]", "m.Fast", "m.Base"} {
		if got := keys[one]; len(got) != 1 || strings.Contains(got[0], cpuOpen) {
			t.Errorf("%s instances %q, want one without a suffix", one, got)
		}
	}
	if f, ok := in.Lookup("m.Fast"); !ok || !slices.Contains(f.CPU, "avx2") {
		t.Errorf("a root's set is its own clause")
	}
}

// TestTransitiveFeatures: the inherited set flows down a chain of
// generic calls (Pick to Inner), not into a fn value made in the cpu body
// (Wider names Pick[i32] as a value: the baseline instance, address taken).
func TestTransitiveFeatures(t *testing.T) {
	sw := newSrcWorld(t, cpuSrc)
	in, ds := Collect(sw, sw.limitRoots(), Options{Target: types.X86_64Windows, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	inner := cpuKeys(in)["m.Inner[i32]"]
	if len(inner) != 2 {
		t.Fatalf("Inner[i32] instances %q, want two", inner)
	}
	for i := range in.List {
		inst := &in.List[i]
		if inst.Name == "m.Pick[i32]" && !inst.AddrTaken {
			t.Errorf("the fn value names the baseline Pick[i32]: want it address taken")
		}
		if strings.HasPrefix(inst.Name, "m.Pick[i32]") && strings.Contains(inst.Name, cpuOpen) && inst.AddrTaken {
			t.Errorf("the avx2 Pick[i32] is address taken: a fn value inherited the set")
		}
	}
	// A freestanding target has no baseline: sse2 from a clause is inherited.
	src := `Twice[T: Plain](x: T): T {
    return x
}

Fpu(x: u64): u64 cpu "sse2" {
    return Twice(x)
}
`
	sw = newSrcWorld(t, src)
	in, ds = Collect(sw, sw.limitRoots(), Options{Target: types.X86_64Freestanding, Validate: true})
	if len(ds) != 0 {
		t.Fatalf("unexpected %v: %s", ds[0].Code, ds[0].Message)
	}
	if got := cpuKeys(in)["m.Twice[u64]"]; len(got) != 1 || !strings.HasSuffix(got[0], " @cpu(sse,sse2)") {
		t.Errorf("freestanding Twice[u64] keys %q, want one with @cpu(sse,sse2)", got)
	}
}
