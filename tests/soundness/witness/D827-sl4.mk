// verdict: reject E0505 E0604
// note: see the comment below. Accepted, it reads or calls dead memory or races when run. Judged by std's TestWitnesses
import std.mem
import std.sim

// sl4: Swap a Sim over the caller's long-lived arena with one over a
// local Fixed, in an inner frame, then end the inner Sim and its arena.
// Swap declares *a into b, *b into a, so outer must take inner's loan of
// local and the Free of local is E0503. If accepted, outer's world arena
// is the one over the dead Fixed.
Inner(sys: &System, outer: mut sim.Sim) {
    buf := sys.arena.Slice[u8](65536) else {
        return
    }
    fixed := mem.Fixed.New(buf)
    var local := Arena.NewIn(fixed.Alloc()) else {
        return
    }
    var inner := sim.Sim.New(&local, 7) else {
        local.Free()
        return
    }
    Swap(outer, mut inner) // <- E0505 E0604
    inner.Free()
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
    var outer := sim.Sim.New(sys.arena, 1).Must()
    Inner(sys, mut outer)
    t := Clobber(3)
    sys.Print("clobbered {t}; asking the Sim's arena for a new chunk\n", (t)).Must()
    big := outer.Arena().Slice[u8](100000) else {
        sys.Print("OutOfMemory from a dead Alloc\n", ()).Must()
        outer.Free()
        return
    }
    sys.Print("got {n} bytes through a dead Alloc\n", (n: big.Len())).Must()
    outer.Free()
}
