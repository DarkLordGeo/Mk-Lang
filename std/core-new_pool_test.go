// Why: Pool's handles must stay safe after their slot is freed or reused, and
// Iter, Sweep and GetPair must see exactly the live slots.
// What: TestCoreNew_PoolIterSweepGetPairMatchModel, against a Go slot-map model.
// How: seeded random Add, Free, Sweep, GetPair and Iter keep every old handle in
// play; the model's LIFO vacant stack checks Iter's order too; dev and release.

package std

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

const corePoolProgram = `import std.mem

Next(s: mut u64): u64 {
    *s = *s *% 6364136223846793005 +% 1442695040888963407
    return *s >> 33
}

export Main(sys: &System): WriteError|OutOfMemory|ParseError!void {
    var out := sys.Stdout()
    n := int(try sys.args[1].ParseInt())
    var s := Trunc[u64](try sys.args[2].ParseInt())
    var p := mem.Pool[int].New(sys.arena)
    var hs := List[mem.Handle].New(sys.arena)
    var v := 0
    for _ in 0..n {
        op := Next(mut s) % 16
        if op <= 5 || hs.Len() == 0 {
            v += 1
            try hs.Push(try p.Add(v))
            try Print(mut out, "add {v}\n", (v))
            continue
        }
        k := Trunc[int](Next(mut s) % Trunc[u64](hs.Len()))
        if op <= 8 {
            ok := p.Free(hs[k])
            try Print(mut out, "free {k} {ok}\n", (k, ok))
        } else if op <= 11 {
            j := Trunc[int](Next(mut s) % Trunc[u64](hs.Len()))
            var got := "none"
            if pr := p.GetPair(hs[k], hs[j]) {
                *pr.a += 1000
                *pr.b += 1
                got = "some"
            }
            try Print(mut out, "pair {k} {j} {got}\n", (k, j, got))
        } else if op <= 13 {
            m := Trunc[int](Next(mut s) % 5) + 2
            r := Trunc[int](Next(mut s) % Trunc[u64](m))
            d := p.Sweep(fn(h: mem.Handle, x: &int): bool { return *x % m != r })
            try Print(mut out, "sweep {m} {r} {d} {l}\n", (m, r, d, l: p.Len()))
        } else {
            try out.Str("iter")
            for (handle, value) in p.Iter() {
                Assert(*p.Get(handle).Must() == *value)
                try Print(mut out, " {value}", (value: *value))
            }
            try out.Str("\n")
        }
    }
}
`

// poolSlot is a slot of the model: the handle it holds (an index into the
// handles ever returned) and its value, or -1 when vacant.
type poolSlot struct {
	h, v int
}

// corePoolModel replays the program's sequence on the model.
func corePoolModel(n int, seed uint64) []string {
	s := seed
	next := func() uint64 {
		s = s*6364136223846793005 + 1442695040888963407
		return s >> 33
	}
	var slots []poolSlot
	var vacant []int
	var handles []int // handle index -> slot, or -1 once freed
	live := 0
	v := 0
	var out []string
	free := func(i int) {
		slots[i].h = -1
		vacant = append(vacant, i)
		live--
	}
	for op := 0; op < n; op++ {
		o := next() % 16
		if o <= 5 || len(handles) == 0 {
			v++
			var i int
			if len(vacant) > 0 {
				i = vacant[len(vacant)-1]
				vacant = vacant[:len(vacant)-1]
				slots[i] = poolSlot{h: len(handles), v: v}
			} else {
				i = len(slots)
				slots = append(slots, poolSlot{h: len(handles), v: v})
			}
			handles = append(handles, i)
			live++
			out = append(out, fmt.Sprintf("add %d", v))
			continue
		}
		k := int(next() % uint64(len(handles)))
		switch {
		case o <= 8:
			i := handles[k]
			ok := i >= 0
			if ok {
				free(i)
				handles[k] = -1
			}
			out = append(out, fmt.Sprintf("free %d %v", k, ok))
		case o <= 11:
			j := int(next() % uint64(len(handles)))
			got := "none"
			if a, b := handles[k], handles[j]; a >= 0 && b >= 0 && a != b {
				slots[a].v += 1000
				slots[b].v++
				got = "some"
			}
			out = append(out, fmt.Sprintf("pair %d %d %s", k, j, got))
		case o <= 13:
			m := int(next()%5) + 2
			r := int(next() % uint64(m))
			d := 0
			for i := range slots {
				if slots[i].h >= 0 && slots[i].v%m == r {
					handles[slots[i].h] = -1
					free(i)
					d++
				}
			}
			out = append(out, fmt.Sprintf("sweep %d %d %d %d", m, r, d, live))
		default:
			var b strings.Builder
			b.WriteString("iter")
			for _, sl := range slots {
				if sl.h >= 0 {
					fmt.Fprintf(&b, " %d", sl.v)
				}
			}
			out = append(out, b.String())
		}
	}
	return out
}

func TestCoreNew_PoolIterSweepGetPairMatchModel(t *testing.T) {
	t.Parallel()
	for _, prof := range [][]string{nil, {"--release"}} {
		exe := buildNative(t, nil, corePoolProgram, prof...)
		for seed := uint64(1); seed <= 30; seed++ {
			n := 40 + int(seed)*41
			r := runNative(t, exe, "", strconv.Itoa(n), strconv.FormatUint(seed*7919, 10))
			if r.code != 0 {
				t.Fatalf("%v seed %d: exit %d\n%s", prof, seed, r.code, r.stderr)
			}
			got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
			want := corePoolModel(n, seed*7919)
			for i := range want {
				if i >= len(got) || got[i] != want[i] {
					t.Fatalf("%v seed %d: first difference at op %d: got %q, want %q", prof, seed, i, poolAt(got, i), want[i])
				}
			}
			if len(got) != len(want) {
				t.Fatalf("%v seed %d: %d lines, want %d", prof, seed, len(got), len(want))
			}
		}
	}
}
