// verdict: reject E1001
// note: the Send hole: an Arena.NewIn arena over a Counting moves into a task while the parent allocates through the same Counting. Accepted, it leaves the Counting at nonzero counts or crashes in Arena.Free when run. Judged by std's TestWitnesses
import std.mem

// p1: an arena over a Counting moved into a task while the parent keeps
// allocating through the same Counting. Must be rejected; if accepted, the
// two tasks bump the Fixed's and the Counting's cells at once.
Work(a: Arena) {
    var b := a
    for _ in 0..20000 {
        _ = b.Dup("racing bytes that will need new chunks soon enough") else {
            break
        }
    }
    b.Free()
}

export Main(sys: &System): WriteError!void {
    buf := sys.arena.Slice[u8](4194304) else {
        return
    }
    fixed := mem.Fixed.New(buf)
    counting := mem.Counting.New(fixed.Alloc())
    arena := Arena.NewIn(counting.Alloc()) else {
        return
    }
    scope s := sys.io.Scope() {
        s.go Work(arena) // <- E1001
        for _ in 0..2000 {
            var xs := HeapList[int].New(counting.Alloc())
            for i in 0..64 {
                xs.Push(counting.Alloc(), i) else {
                    break
                }
            }
            xs.Free(counting.Alloc())
        }
    }
    try sys.Print("live {l} bytes {b} (both 0 when nothing raced)\n", (l: counting.Live(), b: counting.Bytes()))
}
