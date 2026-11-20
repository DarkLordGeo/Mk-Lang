// Why: an accepted stream's peer address comes from the accept op, and the
// Ios other than the native one have no peer to give, so each Io's answer
// must be pinned.
// What: SIM-Accept-Peer: natively the accepted stream names the dialler;
// under a Sim, a Recorder over the native Io and its Replayer it has no
// peer (the zero Addr, Conn.PeerAddr none). SIM-Dial-Unknown: a host the
// Sim does not know is InvalidAddress to net.Dial, as to net.Resolve.
// Helper PeerRun.
// How: PeerRun dials and accepts once on each Io and codes what the
// accepted stream reports. Run by mk test --std=std.sim.

import std.net

// PeerRun dials and accepts once on io: 1 when the accepted stream's peer
// is the dialler's own address and its Conn has it, 2 when the peer is the
// zero Addr and the Conn's PeerAddr is none, 3 for anything else, 4 when a
// call failed.
PeerRun(a: &Arena, io: Io): int {
    l := net.Listen(io, "127.0.0.1:0") else {
        return 4
    }
    defer l.Close()
    addr := a.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
        return 4
    }
    c := net.Dial(io, addr) else {
        return 4
    }
    local := c.LocalAddr()
    c.Close()
    s := l.Accept(io) else {
        return 4
    }
    peer := s.PeerAddr()
    conn := s.Conn()
    defer conn.Abandon()
    zero := peer.port == 0 && !peer.v6 && peer.ip[0] == 0 && peer.ip[3] == 0
    match conn.PeerAddr() {
        some(p) {
            same := p.port == local.port && p.port == peer.port && p.port != 0 && p.v6 == local.v6
            for i in 0..16 {
                if p.ip[i] != local.ip[i] || p.ip[i] != peer.ip[i] {
                    return 3
                }
            }
            return if same { 1 } else { 3 }
        }
        none {
            return if zero { 2 } else { 3 }
        }
    }
}

test "SIM-Accept-Peer natively an accepted stream names the dialler; under a Sim, a Recorder and its Replayer it has no peer" (sys: &System) {
    Assert(PeerRun(sys.arena, sys.io) == 1)
    var s := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    Assert(PeerRun(sys.arena, s.Io()) == 2)
    s.Free()
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    Assert(PeerRun(sys.arena, rec.Io()) == 2)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    Assert(PeerRun(sys.arena, rp.Io()) == 2)
    Assert(!rp.Diverged())
    rp.Free()
}

test "SIM-Dial-Unknown net.Dial of a host name the Sim does not know is InvalidAddress, as net.Resolve reports it" (sys: &System) {
    var s := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    defer s.Free()
    c := net.Dial(s.Io(), "nowhere.test:80") else e {
        Assert(match e {
            net.InvalidAddress { true }
            _ { false }
        })
        return
    }
    c.Close()
    Assert(false)
}
