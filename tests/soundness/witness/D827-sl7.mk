// verdict: reject E0503 E0603
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem
import std.sim

// sl7: s2 with the Sim in a generic struct that is not declared view,
// Pair[T]. Must be rejected (a Pair[sim.Sim] carries the Sim's loans); if
// accepted, a dead Alloc is called.
linear struct Pair[T] {
    x: T
    n: int
}

Make(sys: &System): OutOfMemory!Pair[sim.Sim] {
    buf := try sys.arena.Slice[u8](65536)
    fixed := mem.Fixed.New(buf)
    var arena := try Arena.NewIn(fixed.Alloc())
    s := sim.Sim.New(&arena, 7) else e {
        arena.Free()
        fail e
    }
    p := Pair[sim.Sim](x: s, n: 1)
    arena.Free() // <- E0503
    return p     // <- E0603
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
    p := Make(sys).Must()
    Pair(x, n) := p
    var s := x
    t := Clobber(3)
    sys.Print("clobbered {t} {n}; asking the Sim's arena for a new chunk\n", (t, n)).Must()
    big := s.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        s.Free()
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    s.Free()
}
