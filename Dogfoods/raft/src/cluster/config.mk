// Why: one description of the cluster serves the simulator and real sockets.
// What: Config (the nodes, then the clients), Settings and Parse.
// How: endpoints are numbered; Parse reads a peer list and refuses a count
// past MaxEndpoints.

import std.net
import std.time
import src.raft

/// Endpoints of a cluster: the nodes, then the clients.
export const MaxEndpoints: int = raft.MaxNodes + raft.MaxClients

/// How one run is set up. Every choice the run makes is drawn from seed.
export struct Settings {
    export seed: u64
    export nodes: int
    export clients: int
    /// Operations each client invokes.
    export ops: int
    /// Registers the clients use.
    export keys: int
    /// Datagrams dropped, held back a step, sent twice, and changed in one
    /// byte, each in percent.
    export loss: int
    export delay: int
    export duplicate: int
    export corrupt: int
    /// Messages an endpoint keeps from one step; the newest past it are
    /// dropped.
    export inbox: int
    /// Partitions the network every few hundred milliseconds.
    export partitions: bool
    /// Fails a seeded share of socket and disk operations through the
    /// simulator.
    export faults: bool
    /// The nodes keep their state on the simulator's disk, and the whole
    /// cluster crashes once, at a point drawn from the seed, and restarts.
    export crash: bool
    /// Simulated time the clients run for, then the run ends.
    export duration: time.Duration
    /// Each node runs in a task of its own, in lock step with the network.
    export tasks: bool
    export params: raft.Params
}

/// The defaults: five nodes, three clients of twelve operations on four
/// registers over three seconds, and the paper's timing scaled to a 10 ms
/// step (elections from 150 to 300 ms, heartbeats every 50 ms).
export Settings.Default(seed: u64): Settings {
    return Settings(
        seed: seed,
        nodes: raft.MaxNodes,
        clients: 3,
        ops: 12,
        keys: 4,
        loss: 0,
        delay: 0,
        duplicate: 0,
        corrupt: 0,
        inbox: 256,
        partitions: false,
        faults: false,
        crash: false,
        duration: time.Seconds(3),
        tasks: false,
        params: raft.Params(
            election_min: time.Millis(150).Nanos(),
            election_spread: time.Millis(150).Nanos(),
            heartbeat: time.Millis(50).Nanos(),
            stale_reads: false,
        ),
    )
}

/// A peer list that does not name exactly as many endpoints as the run has.
export error PeerCount(want: int, got: int)

/// The membership: an address for every endpoint, fixed for the run.
export struct Config {
    export addrs: [MaxEndpoints]net.Addr
    export count: int
}

/// The default peers of a cluster of nodes: loopback ports 7001 and up.
/// Fails with OutOfMemory when a cannot hold the text. O(nodes).
export DefaultPeers(a: &Arena, nodes: int): OutOfMemory!str from a {
    var t := Text.New(a)
    for i in 0..nodes {
        if i > 0 {
            try t.Push(",")
        }
        try t.Push(try a.Fmt("127.0.0.1:{port}", (port: 7001 + i)))
    }
    return t.Done()
}

const NoAddr: net.Addr = net.Addr(ip: [0; 16], port: 0, v6: false)

/// Reads a comma-separated peer list, "host:port" each, the host an IP
/// address or a name resolved through io (the first address is taken).
/// Client c listens on the first node's host at port 7101 + c. Fails with
/// PeerCount when the list does not name nodes peers, and as net.ParseAddr
/// or net.Resolve for a bad entry. O(peers); allocates in a.
export Config.Parse(a: &Arena, io: Io, peers: str, nodes: int, clients: int): PeerCount|OutOfMemory|net.Error!Config {
    var c := Config(addrs: [NoAddr; MaxEndpoints], count: nodes + clients)
    var n := 0
    for part in peers.Split(",") {
        if n == nodes {
            fail PeerCount(want: nodes, got: n + 1)
        }
        c.addrs[n] = try Peer(a, io, part.Trim())
        n += 1
    }
    if n != nodes {
        fail PeerCount(want: nodes, got: n)
    }
    for k in 0..clients {
        var addr := c.addrs[0]
        addr.port = Trunc[u16](7101 + k)
        c.addrs[nodes + k] = addr
    }
    return c
}

/// Writes every endpoint's address: "n0=127.0.0.1:7001 ... c0=...".
/// Fails as w does. O(endpoints).
export Config.Describe(&self, w: mut Writer, nodes: int): WriteError!void {
    for i in 0..self.count {
        if i > 0 {
            try w.Str(" ")
        }
        if i < nodes {
            try Print(w, "n{i}=", (i))
        } else {
            try Print(w, "c{c}=", (c: i - nodes))
        }
        try Print(w, "{addr}", (addr: self.addrs[i]))
    }
    try w.Str("\n")
}

// A numeric address parses at once; a name is resolved and keeps the port.
Peer(a: &Arena, io: Io, text: str): OutOfMemory|net.Error!net.Addr {
    addr := net.ParseAddr(text) else e {
        colon := text.Find(":") else {
            fail e
        }
        port := text[colon + 1..].ParseInt() else {
            fail e
        }
        if port < 0 || port > 65535 {
            fail e
        }
        found := try net.Resolve(a, io, text[..colon])
        first := found.Get(0) else {
            fail e
        }
        var resolved := *first
        resolved.port = Trunc[u16](port)
        resolved
    }
    return addr
}
