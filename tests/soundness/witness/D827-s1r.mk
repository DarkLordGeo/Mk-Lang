// verdict: reject E0501
// note: s1 with the arena freed only after the Sim; the Fixed is reset while the Sim, and so the arena it borrows, lives. Judged by std's TestWitnesses
import std.mem
import std.sim

// s1r: s1 with the arena freed only after the Sim, so the arena's own
// lifetime is legal; the Fixed is reset while the Sim (and so the arena it
// borrows) lives. Must be rejected at fixed.Reset().
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
    msg := s.Arena().Dup("the simulated world's own bytes") else {
        s.Free()
        arena.Free()
        return
    }
    fixed.Reset() // <- E0501
    sys.Print("after reset: {m}\n", (m: msg)).Must()
    s.Free()
    arena.Free()
}
