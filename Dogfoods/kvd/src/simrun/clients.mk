// Why: the simulation needs clients whose every reply can be checked.
// What: Clients, Client, Shutdown and Agrees.
// How: each client runs its steps over its own connection and keeps what it
// was told; then a last connection stops the server with SHUTDOWN.

import src.client
import src.server
import src.store

/// Runs `n` clients side by side, then stops the server with `SHUTDOWN`, as
/// an operator would, unless a crash stopped it first. Runs as a task.
Clients(s: &Scope, st: &server.State, heap: &Heap, io: Io, addr: str, seed: u64, n: int, steps: int, partial: bool, outcomes: mut []Outcome) {
    scope c := io.Scope() {
        var i := 0
        for piece in outcomes.ChunksMut(1) {
            c.go Client(heap, io, addr, seed, i, steps, partial && i == 0, piece)
            i += 1
        }
    }
    if st.Crashed() {
        return
    }
    if !Shutdown(io, addr) {
        st.Stop(io)
        s.Cancel()
    }
}

/// Sends `SHUTDOWN`; false when the server could not be told.
Shutdown(io: Io, addr: str): bool {
    conn := client.Connect(io, addr, 5) else {
        return false
    }
    defer conn.Close()
    var inbuf: [256]u8 = [0; 256]
    var outbuf: [256]u8 = [0; 256]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    w.Str("SHUTDOWN\n") else {
        return false
    }
    w.Flush() else {
        return false
    }
    var line: [64]u8 = [0; 64]
    got := r.Until(b'\n', mut line[..]) else {
        return false
    }
    return got > 0 && line[0] == b'+'
}

/// One client: its steps in order, each answered before the next is sent.
/// Reads are checked against the client's own model of its keys. Runs as a
/// task and writes what happened into `out[0]`.
Client(heap: &Heap, io: Io, addr: str, seed: u64, i: int, steps: int, partial: bool, out: mut []Outcome) {
    a := heap.Alloc()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var list: [MaxSteps]Step = [Step(kind: Kind.Get, key: 0); MaxSteps]
    Steps(seed, i, mut list[..steps])
    conn := client.Connect(io, addr, 5) else e {
        out[0].stopped = some(e)
        return
    }
    defer conn.Close()
    var model := store.Store.NewFixed(a)
    defer model.Free(a)
    var inbuf: [1024]u8 = [0; 1024]
    var outbuf: [1024]u8 = [0; 1024]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    for j in 0..steps {
        arena.Reset()
        if partial && j == 2 {
            // leave in the middle of a command: the server must not run it
            w.Str("SET c0k0 half-a-comm") else {}
            w.Flush() else {}
            out[0].gone = true
            return
        }
        step := &list[j]
        request := Request(&arena, seed, i, j, step) else {
            return
        }
        out[0].sent = j + 1
        lines := client.Call(&arena, mut r, mut w, request) else e {
            out[0].stopped = some(e)
            return
        }
        out[0].answered = j + 1
        reply := lines[0]
        if reply.StartsWith("-") {
            out[0].refused |= Cast[u64](1) << Cast[u64](j)
            continue
        }
        if !Agrees(&arena, &model, i, step, reply) && out[0].misread < 0 {
            out[0].misread = j
        }
        ApplyStep(a, &arena, mut model, seed, i, j, step) else {
            return
        }
    }
    _ = client.Call(&arena, mut r, mut w, "QUIT") else {}
}

/// Whether a reply agrees with the client's model: a `GET` must read what
/// the client last wrote to its key. Other replies are not checked here.
Agrees(arena: &Arena, model: &store.Store, client: int, step: &Step, reply: str): bool {
    match step.kind {
        .Get {}
        _ {
            return true
        }
    }
    name := KeyName(arena, client, step.key) else {
        return true
    }
    want := model.Get(name) else {
        return reply == "_"
    }
    return reply.StartsWith("$") && reply[1..] == want
}
