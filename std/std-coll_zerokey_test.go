// Why: no prelude type of size zero has Ord, so B-trees with a zero-size key
// need their own test.
// What: TestSTDCOLL_ZeroSizeKey: BTree and HeapBTree keyed by ZK, an empty
// struct whose keys are all equal, with int and zero-size values.
// How: every Set after the first replaces the one entry; Len stays 1, Range is
// empty, and Remove empties the tree, which a refill reuses. Dev and release.

package std

import "testing"

const c2ZeroKeyProgram = `
import std.coll

struct ZK {
}

impl Ord for ZK {
    Less(&self, other: &ZK): bool {
        return false
    }
}

struct Z {
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    h := sys.heap.Alloc()
    var t := coll.BTree[ZK, int].New(sys.arena)
    var u := coll.HeapBTree[ZK, int].New(h)
    onerr u.Free(h)
    for i in 0..1000 {
        try t.Set(ZK(), i)
        try u.Set(h, ZK(), i + 1)
    }
    tl := t.Len()
    ul := u.Len()
    var tg := -1
    if v := t.Get(&ZK()) {
        tg = *v
    }
    var ug := -1
    if v := u.Get(&ZK()) {
        ug = *v
    }
    if v := t.GetMut(&ZK()) {
        *v += 5
    }
    if v := u.GetMut(&ZK()) {
        *v += 7
    }
    var n := 0
    var sum := 0
    var it := t.Iter()
    for e := it.Next() {
        n += 1
        sum += *e.value
    }
    var it2 := u.Iter()
    for e := it2.Next() {
        n += 1
        sum += *e.value
    }
    var rn := 0
    var r := t.Range(&ZK(), &ZK())
    for e := r.Next() {
        rn += 1 + *e.value
    }
    var r2 := u.Range(&ZK(), &ZK())
    for e := r2.Next() {
        rn += 1 + *e.value
    }
    has := t.Has(&ZK()) && u.Has(&ZK())
    try Print(mut out, "set {tl} {ul} {tg} {ug} {n} {sum} {rn} {has}\n", (tl, ul, tg, ug, n, sum, rn, has))
    var rt := -1
    if v := t.Remove(&ZK()) {
        rt = v
    }
    var ru := -1
    if v := u.Remove(&ZK()) {
        ru = v
    }
    var again := 0
    if t.Remove(&ZK()).IsSome() {
        again += 1
    }
    if u.Remove(&ZK()).IsSome() {
        again += 1
    }
    tl2 := t.Len()
    ul2 := u.Len()
    has2 := t.Has(&ZK()) || u.Has(&ZK())
    try Print(mut out, "remove {rt} {ru} {again} {tl2} {ul2} {has2}\n", (rt, ru, again, tl2, ul2, has2))
    for i in 0..10 {
        try t.Set(ZK(), 100 + i)
        try u.Set(h, ZK(), 200 + i)
    }
    var zz := coll.BTree[ZK, Z].New(sys.arena)
    var hz := coll.HeapBTree[ZK, Z].New(h)
    onerr hz.Free(h)
    for _ in 0..50 {
        try zz.Set(ZK(), Z())
        try hz.Set(h, ZK(), Z())
    }
    var zn := 0
    var zi := zz.Iter()
    for e := zi.Next() {
        _ = e
        zn += 1
    }
    tl3 := t.Len()
    ul3 := u.Len()
    var tg3 := -1
    if v := t.Get(&ZK()) {
        tg3 = *v
    }
    var ug3 := -1
    if v := u.Get(&ZK()) {
        ug3 = *v
    }
    zl := zz.Len()
    hl := hz.Len()
    zr := zz.Remove(&ZK()).IsSome()
    zl2 := zz.Len()
    try Print(mut out, "refill {tl3} {ul3} {tg3} {ug3} {zl} {hl} {zn} {zr} {zl2}\n", (tl3, ul3, tg3, ug3, zl, hl, zn, zr, zl2))
    u.Free(h)
    hz.Free(h)
}
`

// TestSTDCOLL_ZeroSizeKey: a BTree and a HeapBTree keyed by an empty struct
// whose keys are all equal hold one entry with the last value set (999 and
// 1000), GetMut updates it in place (+5, +7), Iter yields it once in each
// tree (sum 1004 + 1007 = 2011), Range(lo, hi) with lo == hi is empty,
// Remove returns the value and leaves both trees empty, a second Remove
// finds nothing, a refill keeps the last value (109, 209), and a key and
// value both of size zero keep one entry.
func TestSTDCOLL_ZeroSizeKey(t *testing.T) {
	t.Parallel()
	const want = "set 1 1 999 1000 2 2011 0 true\n" +
		"remove 1004 1007 0 0 0 false\n" +
		"refill 1 1 109 209 1 1 1 true 0\n"
	for _, flags := range c2Flags {
		t.Run(c2FlagName(flags), func(t *testing.T) {
			t.Parallel()
			diffLines(t, collRun(t, c2ZeroKeyProgram, flags...), want)
		})
	}
}
