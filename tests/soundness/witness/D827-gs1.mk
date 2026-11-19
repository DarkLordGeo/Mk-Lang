// verdict: reject E1001
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem

// gs1: the arena as the receiver of the spawned call, not an argument:
// s.go arena.Free() moves it into the task, whose Free returns every chunk
// to the Counting while the parent allocates through the same Counting.
// Must be rejected (E1001 on the receiver, concurrency.go's parts).
export Main(sys: &System): WriteError!void {
    buf := sys.arena.Slice[u8](4194304) else {
        return
    }
    fixed := mem.Fixed.New(buf)
    counting := mem.Counting.New(fixed.Alloc())
    var arena := Arena.NewIn(counting.Alloc()) else {
        return
    }
    for _ in 0..20000 {
        _ = arena.Dup("bytes that need new chunks from the Counting") else {
            break
        }
    }
    scope s := sys.io.Scope() {
        s.go arena.Free() // <- E1001
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
