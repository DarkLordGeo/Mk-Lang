// verdict: reject E0503
// note: the Sim hole: a Sim over an Arena.NewIn arena over a Fixed outlives the arena, then the Fixed is reset and refilled by a second arena. Accepted, it prints CCCC... for the Sim's own bytes when run, then crashes in Arena.Free. Judged by std's TestWitnesses
import std.mem
import std.sim

// s1: Sim.New keeps a.Sub() in its world and holds no loan of a (nothing
// of a itself is kept, so a holds no loan of the Sim and the Sim
// none of a). Over an Arena.NewIn arena the Sub's chunks are the Fixed's
// bytes, so the Fixed can be reset while the Sim's arena still uses them.
// Must be rejected; if accepted, msg reads bytes another arena now owns.
export Main(sys: &System) {
    buf := sys.arena.Slice[u8](65536) else {
        return
    }
    var fixed := mem.Fixed.New(buf)
    var arena := Arena.NewIn(fixed.Alloc()) else {
        return
    }
    var s := sim.Sim.New(&arena, 7) else {
        arena.Free()
        return
    }
    arena.Free() // <- E0503
    msg := s.Arena().Dup("the simulated world's own bytes") else {
        s.Free()
        return
    }
    sys.Print("before reset: {m}\n", (m: msg)).Must()
    fixed.Reset()
    var other := Arena.NewIn(fixed.Alloc()) else {
        s.Free()
        return
    }
    fill := other.Slice[u8](6000) else {
        other.Free()
        s.Free()
        return
    }
    for i in 0..fill.Len() {
        fill[i] = 0x43
    }
    sys.Print("after reset:  {m}\n", (m: msg)).Must()
    other.Free()
    sys.Print("freeing the Sim, whose arena header is now 'C' bytes\n", ()).Must()
    s.Free()
    sys.Print("not reached\n", ()).Must()
}
