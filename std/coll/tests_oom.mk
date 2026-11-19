// Why: a B-tree Set that fails half-way must leave the tree as it was; the
// other fault-injection tests reach the trees only through Replace.
// What: OutOfMemory tests of HeapBTree.Set and BTree.Set over a failing heap.
// How: the heap fails exactly the at-th alloc or growing resize, for every at
// until a run meets none; each failed Set must keep the tree's length,
// contents and live bytes and hand k and v back, the retried Set succeeds,
// and the final tree holds every entry. Helper names start with Co, because
// every file of std/coll is one module.

import std.core
import std.raw

struct CoFail {
    inner: *Alloc
    live: int
    reqs: int
    at: int
    fails: int
}

const CoFailTable = core.AllocTable(alloc: CoFailAlloc, resize: CoFailResize, free: CoFailFree)

CoFailAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is a live CoFail whose inner allocator outlives it" {
        q := raw.PtrCast[u8, CoFail](ctx)
        var c := raw.Read(q)
        c.reqs += 1
        var p := raw.Null[u8]()
        if c.reqs - 1 == c.at {
            c.fails += 1
        } else {
            p = core.AllocBytes(&*c.inner, size, align)
            if !raw.IsNull(p) {
                c.live += size
            }
        }
        raw.Write(q, c)
        return p
    }
}

CoFailResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    unsafe "ctx is a live CoFail whose inner allocator outlives it" {
        q := raw.PtrCast[u8, CoFail](ctx)
        var c := raw.Read(q)
        var ok := false
        var refused := false
        if new > old {
            c.reqs += 1
            if c.reqs - 1 == c.at {
                c.fails += 1
                refused = true
            }
        }
        if !refused {
            ok = core.ResizeBytes(&*c.inner, p, old, new, align)
            if ok {
                c.live += new - old
            }
        }
        raw.Write(q, c)
        return ok
    }
}

CoFailFree(ctx: *u8, p: *u8, size: int, align: int) {
    unsafe "ctx is a live CoFail whose inner allocator outlives it" {
        q := raw.PtrCast[u8, CoFail](ctx)
        var c := raw.Read(q)
        core.FreeBytes(&*c.inner, p, size, align)
        c.live -= size
        raw.Write(q, c)
    }
}

// CoLive, CoReqs and CoFails read c: its live bytes, requests and failures
// met.
CoLive(c: &CoFail): int {
    unsafe "c is live" {
        return raw.Read(raw.Of(c)).live
    }
}

CoReqs(c: &CoFail): int {
    unsafe "c is live" {
        return raw.Read(raw.Of(c)).reqs
    }
}

CoFails(c: &CoFail): int {
    unsafe "c is live" {
        return raw.Read(raw.Of(c)).fails
    }
}

// CoRes is linear and Free, so HeapBTree.Set takes it and a
// failed Set must hand it back.
linear struct CoRes {
    n: int
}

impl Free for CoRes {
    Free(self, a: &Alloc) {
        CoRes(n: _) := self
    }
}

CoTake(r: CoRes): int {
    CoRes(n) := r
    return n
}

// CoRun is one run's result: failures met, Sets that failed, the length at
// the last failure, and the requests the run made.
struct CoRun {
    fails: int
    oom: int
    deep: int
    reqs: int
}

// CoN is the number of keys: i * 7919 % CoN for i in 0..CoN is a
// permutation of 0..CoN, since 7919 and 1009 are distinct primes.
const CoN = 1009

// CoHeapSet fills a HeapBTree[int, CoRes] by Set over a heap that fails its
// at-th request (none when at is negative), then overwrites every key by
// Set, which allocates nothing, and frees the tree.
CoHeapSet(a: &Alloc, at: int): CoRun {
    var c := CoFail(inner: raw.Of(a), live: 0, reqs: 0, at: at, fails: 0)
    unsafe "c outlives the heap made over it, freed below" {
        h := core.MakeHeap(raw.PtrCast[CoFail, u8](raw.Of(&c)), raw.Of(&CoFailTable))
        ha := h.Alloc()
        var t := HeapBTree[int, CoRes].New(ha)
        var oom := 0
        var deep := -1
        for i in 0..CoN {
            k := i * 7919 % CoN
            n := t.Len()
            before := CoLive(&c)
            v := CoRes(n: i)
            t.Set(ha, k, v) else {
                oom += 1
                deep = n
                Assert(CoTake(v) == i)
                Assert(t.Len() == n && t.Get(&k).IsNone() && CoLive(&c) == before)
                w := CoRes(n: i)
                t.Set(ha, k, w) else {
                    Assert(CoTake(w) < 0)
                }
            }
        }
        Assert(t.Len() == CoN)
        live := CoLive(&c)
        reqs := CoReqs(&c)
        for i in 0..CoN {
            k := i * 7919 % CoN
            Assert(t.Get(&k).Must().n == i)
            v := CoRes(n: CoN + i)
            t.Set(ha, k, v) else {
                Assert(CoTake(v) < 0)
            }
        }
        Assert(t.Len() == CoN && CoLive(&c) == live && CoReqs(&c) == reqs)
        for k in 0..CoN {
            Assert(t.Get(&k).Must().n >= CoN)
        }
        t.Free(ha)
        Assert(CoLive(&c) == 0)
        return CoRun(fails: CoFails(&c), oom: oom, deep: deep, reqs: reqs)
    }
}

// CoArenaSet is CoHeapSet for an arena BTree[int, int] whose arena takes its
// pages from the failing heap; a failed page is the Set's OutOfMemory, or
// Arena.New's, which the run reports with deep -2.
CoArenaSet(a: &Alloc, at: int): CoRun {
    var c := CoFail(inner: raw.Of(a), live: 0, reqs: 0, at: at, fails: 0)
    unsafe "c outlives the heap made over it, freed below" {
        h := core.MakeHeap(raw.PtrCast[CoFail, u8](raw.Of(&c)), raw.Of(&CoFailTable))
        var ar := Arena.New(&h) else {
            Assert(CoFails(&c) == 1)
            return CoRun(fails: 1, oom: 0, deep: -2, reqs: CoReqs(&c))
        }
        var t := BTree[int, int].New(&ar)
        var oom := 0
        var deep := -1
        for i in 0..CoN {
            k := i * 7919 % CoN
            n := t.Len()
            t.Set(k, i) else {
                oom += 1
                deep = n
                Assert(t.Len() == n && t.Get(&k).IsNone())
                t.Set(k, i) else {
                    Assert(false)
                }
            }
        }
        Assert(t.Len() == CoN)
        reqs := CoReqs(&c)
        for i in 0..CoN {
            k := i * 7919 % CoN
            Assert(*t.Get(&k).Must() == i)
            t.Set(k, -i) else {
                Assert(false)
            }
        }
        Assert(t.Len() == CoN && CoReqs(&c) == reqs)
        for i in 0..CoN {
            k := i * 7919 % CoN
            Assert(*t.Get(&k).Must() == -i)
        }
        ar.Free()
        Assert(CoLive(&c) == 0)
        return CoRun(fails: CoFails(&c), oom: oom, deep: deep, reqs: reqs)
    }
}

test "HeapBTree.Set under OutOfMemory keeps the tree and hands back k and v" (sys: &System) {
    a := sys.heap.Alloc()
    ref := CoHeapSet(a, -1)
    Assert(ref.fails == 0 && ref.oom == 0 && ref.reqs > 2)
    var at := 0
    var deep := 0
    for {
        r := CoHeapSet(a, at)
        if r.fails == 0 {
            break
        }
        Assert(r.fails == 1 && r.oom == 1)
        if r.deep > deep {
            deep = r.deep
        }
        at += 1
    }
    // Every request of the clean run was one Set's failure, the last of them
    // with over 600 entries in the tree, so splits below the root were met.
    Assert(at == ref.reqs && deep > 600)
}

test "BTree.Set under OutOfMemory keeps the tree" (sys: &System) {
    a := sys.heap.Alloc()
    ref := CoArenaSet(a, -1)
    Assert(ref.fails == 0 && ref.oom == 0 && ref.reqs > 1)
    var at := 0
    var met := 0
    var pages := 0
    for {
        r := CoArenaSet(a, at)
        if r.fails == 0 {
            break
        }
        Assert(r.fails == 1)
        if r.deep == -2 {
            pages += 1
        }
        met += r.oom
        at += 1
    }
    // Every request of the clean run failed once: Arena.New's own page
    // requests (deep -2), and every other one a Set.
    Assert(at == ref.reqs && met + pages == at && met > 0)
}
