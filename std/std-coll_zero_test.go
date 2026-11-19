// Why: a zero-size element takes no memory (as GrowSlots), so container
// arithmetic that divides or multiplies by the size must still count right.
// What: TestSTDCOLL_ZeroSize: Deque, HeapDeque, BTree and HeapBTree of an
// empty struct.
// How: grow, wrap, pop, split and merge, with the same counts as a sized
// element, at dev and release.

package std

import "testing"

const c2ZeroProgram = `
import std.coll

struct Z {
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    h := sys.heap.Alloc()
    var d := coll.Deque[Z].New(sys.arena)
    var q := coll.HeapDeque[Z].New(h)
    onerr q.Free(h)
    for i in 0..1000 {
        if i % 3 == 0 {
            try d.PushFront(Z())
            try q.PushFront(h, Z())
        } else {
            try d.Push(Z())
            try q.Push(h, Z())
        }
    }
    var popped := 0
    for i in 0..400 {
        if i % 2 == 0 {
            if d.PopFront().IsSome() {
                popped += 1
            }
        } else {
            if d.Pop().IsSome() {
                popped += 1
            }
        }
    }
    dl := d.Len()
    ql := q.Len()
    g := d.Get(599).IsSome()
    g2 := d.Get(600).IsSome()
    try Print(mut out, "deque {dl} {ql} {popped} {g} {g2}\n", (dl, ql, popped, g, g2))
    var t := coll.BTree[int, Z].New(sys.arena)
    var u := coll.HeapBTree[int, Z].New(h)
    onerr u.Free(h)
    for i in 0..2000 {
        k := (i * 7919) % 2000
        try t.Set(k, Z())
        try u.Set(h, k, Z())
    }
    for i in 0..2000 {
        if i % 2 == 0 {
            _ = t.Remove(&i)
            _ = u.Remove(&i)
        }
    }
    var n := 0
    var sum := 0
    var it := t.Iter()
    for e := it.Next() {
        n += 1
        sum += *e.key
    }
    tl := t.Len()
    ul := u.Len()
    has := t.Get(&1).IsSome()
    try Print(mut out, "btree {tl} {ul} {n} {sum} {has}\n", (tl, ul, n, sum, has))
    q.Free(h)
    u.Free(h)
}
`

// TestSTDCOLL_ZeroSize: 1,000 pushes at both ends and 400 pops of a
// zero-size element (Get past the end is none), and 2,000 sets and 1,000
// removes of a BTree with a zero-size value, with the iteration's key sum.
func TestSTDCOLL_ZeroSize(t *testing.T) {
	t.Parallel()
	const want = "deque 600 1000 400 true false\nbtree 1000 1000 1000 1000000 true\n"
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, collRun(t, c2ZeroProgram, flags...), want)
		})
	}
}
