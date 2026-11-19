// verdict: reject E0505
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem
import std.sim

// ch1: one task, a Chan on the heap made by Main; Inner sends a Sim over
// an arena on a local Fixed into it and ends the arena; Main receives the
// Sim afterwards. Chan.Send takes x into self, so the send must store the
// loan of local into the channel, which outlives it. Must be rejected
// (E0505 or E0503); if accepted, Main's Sim asks a dead Alloc for a chunk.
Inner(sys: &System, ch: &Chan[sim.Sim]) {
    buf := sys.arena.Slice[u8](65536) else {
        return
    }
    fixed := mem.Fixed.New(buf)
    var local := Arena.NewIn(fixed.Alloc()) else {
        return
    }
    s := sim.Sim.New(&local, 7) else {
        local.Free()
        return
    }
    ch.Send(sys.io, s).Must() // <- E0505
    local.Free()
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
    ch := Chan[sim.Sim].New(sys.heap.Alloc(), 1).Must()
    Inner(sys, &ch)
    t := Clobber(3)
    sys.Print("clobbered {t}; asking the Sim's arena for a new chunk\n", (t)).Must()
    var s := ch.Recv(sys.io) else {
        ch.FreeEmpty(sys.heap.Alloc())
        return
    }
    big := s.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        s.Free()
        ch.FreeEmpty(sys.heap.Alloc())
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    s.Free()
    ch.FreeEmpty(sys.heap.Alloc())
}
