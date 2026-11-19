// verdict: reject E0503 E0505 E0603
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem
import std.sim

// sl1: s2 with the Sim inside a HeapList: Make owns the Fixed, makes a Sim
// over an arena on it and returns the Sim in a HeapList on the heap. Must
// be rejected (Push stores x into self, so the list holds the Sim's loan
// of arena); if accepted, the Sim's arena asks a dead Alloc for a chunk.
Make(sys: &System): OutOfMemory!HeapList[sim.Sim] {
    buf := try sys.arena.Slice[u8](65536)
    fixed := mem.Fixed.New(buf)
    var arena := try Arena.NewIn(fixed.Alloc())
    s := sim.Sim.New(&arena, 7) else e {
        arena.Free()
        fail e
    }
    var xs := HeapList[sim.Sim].New(sys.heap.Alloc())
    xs.Push(sys.heap.Alloc(), s).Must() // <- E0505
    arena.Free()                        // <- E0503
    return xs                           // <- E0603
}

Clobber(n: int): int {
    var junk: [8192]u8 = [0x5A; 8192]
    var t := 0
    for i in 0..junk.Len() {
        junk[i] = Trunc[u8](i + n)
        t += int(junk[i])
    }
    return t
}

export Main(sys: &System) {
    var xs := Make(sys).Must()
    t := Clobber(3)
    sys.Print("clobbered {t}; asking the Sim's arena for a new chunk\n", (t)).Must()
    var s := xs.Pop() else {
        xs.FreeEmpty(sys.heap.Alloc())
        return
    }
    big := s.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        s.Free()
        xs.FreeEmpty(sys.heap.Alloc())
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    s.Free()
    xs.FreeEmpty(sys.heap.Alloc())
}
