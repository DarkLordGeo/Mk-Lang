// verdict: reject E0503 E0603
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem
import std.sim

// sl2: s2 with the Sim in an optional, ?sim.Sim. Must be rejected (E0603
// on the return, E0503 on the Free); if accepted, a dead Alloc is called.
Make(sys: &System): ?sim.Sim {
    buf := sys.arena.Slice[u8](65536) else {
        return none
    }
    fixed := mem.Fixed.New(buf)
    var arena := Arena.NewIn(fixed.Alloc()) else {
        return none
    }
    s := sim.Sim.New(&arena, 7) else {
        arena.Free()
        return none
    }
    arena.Free()   // <- E0503
    return some(s) // <- E0603
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
    var s := Make(sys) else {
        return
    }
    t := Clobber(3)
    sys.Print("clobbered {t}; asking the Sim's arena for a new chunk\n", (t)).Must()
    big := s.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        s.Free()
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    s.Free()
}
