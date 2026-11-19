// verdict: reject E0503 E0603
// note: the Sim hole: a Sim over an Arena.NewIn arena leaves the frame that owns the Fixed. Accepted, it calls the dead Alloc when run (a panic at std/core/mem_internal.mk, AllocBytes). Judged by std's TestWitnesses
import std.mem
import std.sim

// s2: the Sim leaves the frame that owns the Fixed. Sim.New's result holds
// no loan of a, so Make may return it; the Sim's arena keeps the address of
// the Fixed's Alloc, in Make's dead frame. Must be rejected; if accepted,
// the next chunk is asked of a dead Alloc.
Make(sys: &System): OutOfMemory!sim.Sim {
    buf := try sys.arena.Slice[u8](65536)
    fixed := mem.Fixed.New(buf)
    var arena := try Arena.NewIn(fixed.Alloc())
    s := sim.Sim.New(&arena, 7) else e {
        arena.Free()
        fail e
    }
    arena.Free() // <- E0503
    return s     // <- E0603
}

// Clobber writes over the stack Make used.
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
    var s := Make(sys).Must()
    t := Clobber(3)
    sys.Print("clobbered {t}; asking the Sim's arena for a new chunk\n", (t)).Must()
    big := s.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        s.Free()
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    s.Free()
    sys.Print("freed\n", ()).Must()
}
