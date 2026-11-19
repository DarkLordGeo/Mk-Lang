// Why: sync.Shared's box must be freed by exactly the last Free, whatever
// the order and whichever task frees.
// What: two blocks over Shared's Clone and Free, in one task and in 8.
// How: run by `mk test --std=std.sync` (std/std-sync_regress_test.go,
// TestSTDSYNC_TestBlocks) in a child process with leak detection.

// A box the last Free did not free fails the block. "Shared.Free of the
// last handle of a linear T panics with code 8" is a Go test
// (TestSTDSYNC_SharedAcrossTasks), as a test that must panic cannot be a
// passing block.

test "Clone and Free; the last handle releases the box" (sys: &System) {
    p := Shared[int].New(sys.heap, 5) else {
        Assert(false)
        return
    }
    q := p.Clone()
    r := q.Clone()
    Assert(*p.Ref() == 5)
    Assert(*q.Ref() == 5)
    Assert(*r.Ref() == 5)
    // Any order: the box goes with whichever Free is last.
    q.Free(sys.heap)
    p.Free(sys.heap)
    Assert(*r.Ref() == 5)
    r.Free(sys.heap)
}

Reader(p: Shared[int], h: &Heap, io: Io) {
    io.Yield() else {}
    Assert(*p.Ref() == 7)
    p.Free(h)
}

test "8 tasks each free their own handle" (sys: &System) {
    for _ in 0..20 {
        p := Shared[int].New(sys.heap, 7) else {
            Assert(false)
            return
        }
        scope s := sys.io.Scope() {
            for _ in 0..8 {
                s.go Reader(p.Clone(), sys.heap, sys.io)
            }
            // Main's handle goes first, inside the scope, so the last Free
            // runs in whichever task ends last.
            p.Free(sys.heap)
        }
    }
}
