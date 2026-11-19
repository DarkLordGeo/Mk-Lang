// Why: a random stream never produces the value orders that stress a heap's
// sift-up and its bottom-up pop (ascending, descending, all equal, sawtooth).
// What: TestSTDCOLL_PriorityQueueAdversarial, against container/heap
// and a sort.
// How: the size sweep 1 to 40 drains each heap, so Pop runs wherever the
// last internal node has 1 to 4 children.

package std

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
	"testing"
)

const c2PQAdvProgram = `import std.coll

// CONSTS

// Val is the value of push op: ascending, descending, all equal, sawtooth
// or random with ties.
Val(mode: int, op: int, r: u64): int {
    if mode == 1 {
        return op
    } else if mode == 2 {
        return OPS - op
    } else if mode == 3 {
        return 5
    } else if mode == 4 {
        return (op % 7) * 100 - op
    }
    return Trunc[int]((r >> 8) % 1000)
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    for mode in 1..5 {
        var q := coll.PriorityQueue[int].New(sys.arena)
        var s: u64 = SEED
        for op in 0..OPS {
            s = s +% GOLD
            r := Mix(s)
            var what := Trunc[int](r & 7)
            if (op / PHASE) % 2 == 1 && what < 3 {
                what = 4
            }
            if what < 4 {
                try q.Push(Val(mode, op, r))
            } else if what < 6 {
                a := q.Pop() else { -1 }
                try Print(mut out, "O {a}\n", (a))
            } else if what == 6 {
                a := Opt(q.Peek())
                try Print(mut out, "K {a}\n", (a))
            } else {
                n := q.Len()
                try Print(mut out, "L {n}\n", (n))
            }
        }
        var sum := 0
        for x := q.Pop() {
            sum = sum *% 31 +% x
        }
        try Print(mut out, "E {mode} {sum}\n", (mode, sum))
    }
    for mode in 0..5 {
        for n in 1..41 {
            var q := coll.PriorityQueue[int].New(sys.arena)
            var s: u64 = SEED +% Trunc[u64](n)
            for op in 0..n {
                s = s +% GOLD
                try q.Push(Val(mode, op, Mix(s)))
            }
            var sum := 0
            var cnt := 0
            var bad := 0
            for {
                k := Opt(q.Peek())
                x := q.Pop() else {
                    break
                }
                if x != k {
                    bad += 1
                }
                cnt += 1
                sum = sum *% 31 +% x
            }
            left := q.Len()
            try Print(mut out, "S {mode} {n} {cnt} {sum} {bad} {left}\n", (mode, n, cnt, sum, bad, left))
        }
    }
}
`

func c2PQAdvVal(mode, op, ops int, r uint64) int {
	switch mode {
	case 1:
		return op
	case 2:
		return ops - op
	case 3:
		return 5
	case 4:
		return (op%7)*100 - op
	}
	return int((r >> 8) % 1000)
}

func c2PQAdvModel(seed uint64, ops, phase int) string {
	var b strings.Builder
	for mode := 1; mode < 5; mode++ {
		h := &c2IntHeap{}
		s := seed
		for op := 0; op < ops; op++ {
			s += c2Gold
			r := c2Mix(s)
			what := int(r & 7)
			if (op/phase)%2 == 1 && what < 3 {
				what = 4
			}
			switch {
			case what < 4:
				heap.Push(h, c2PQAdvVal(mode, op, ops, r))
			case what < 6:
				a := -1
				if h.Len() > 0 {
					a = heap.Pop(h).(int)
				}
				fmt.Fprintf(&b, "O %d\n", a)
			case what == 6:
				a := -1
				if h.Len() > 0 {
					a = (*h)[0]
				}
				fmt.Fprintf(&b, "K %d\n", a)
			default:
				fmt.Fprintf(&b, "L %d\n", h.Len())
			}
		}
		sum := 0
		for h.Len() > 0 {
			sum = sum*31 + heap.Pop(h).(int)
		}
		fmt.Fprintf(&b, "E %d %d\n", mode, sum)
	}
	for mode := 0; mode < 5; mode++ {
		for n := 1; n <= 40; n++ {
			s := seed + uint64(n)
			var xs []int
			for op := 0; op < n; op++ {
				s += c2Gold
				xs = append(xs, c2PQAdvVal(mode, op, ops, c2Mix(s)))
			}
			sort.Ints(xs)
			sum := 0
			for _, x := range xs {
				sum = sum*31 + x
			}
			fmt.Fprintf(&b, "S %d %d %d %d 0 0\n", mode, n, n, sum)
		}
	}
	return b.String()
}

// TestSTDCOLL_PriorityQueueAdversarial: ascending, descending,
// all-equal and sawtooth values in PriorityQueueDiff's push and pop mix,
// against container/heap; then every size 1 to 40 in each order (and
// random with ties) pushed and drained, each pop equal to the Peek before
// it, against a sort. Dev and release.
//
// Ascending pushes never sift up; descending pushes sift up to the root every
// time; all-equal values make the bottom-up pop's sift-up start at a leaf with
// every compare failing; a sawtooth mixes runs. The size sweep makes Pop run
// at every n where the last internal node has 1, 2, 3 or 4 children, which is
// where the descent's bound (n + 2) >> 2 and the child window min(c + 4, n)
// matter.
func TestSTDCOLL_PriorityQueueAdversarial(t *testing.T) {
	t.Parallel()
	const seed, ops, phase = 0xADD, 20000, 2500
	src := c2Program(c2PQAdvProgram, map[string]string{
		"SEED: u64": fmt.Sprint(seed), "OPS": fmt.Sprint(ops), "PHASE": fmt.Sprint(phase)})
	want := c2PQAdvModel(seed, ops, phase)
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, collRun(t, src, flags...), want)
		})
	}
}
