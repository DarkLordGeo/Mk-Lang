// Why: The server starts helper clients in its own process, so a session has
// someone to talk to and the bots use the same protocol path as outside
// clients.
//
// What: BotNames, Bot (the echo and stats bots), Answer, and the reply helpers
// Await and Ask that the load and replay clients share.
//
// How: Each bot connects over loopback, joins `#bots`, meets the other bots and
// Main at the hub's barrier, then answers lines in a loop.

import std.atomic
import std.net
import src.hub

/// The bots' names; `--bots N` starts the first N.
const BotNames: [2]str = ["echo-bot", "stats-bot"]

/// Connects bot `i` and joins it to `#bots`, then meets the other bots and
/// `Main` at the hub's barrier, so no client is served before every bot is
/// in. The echo bot repeats `!echo text`; the stats bot answers `!stats`.
/// Ends when the server closes the connection.
export Bot(io: Io, addr: str, hb: &hub.Hub, i: int) {
    conn := net.Dial(io, addr) else {
        _ = hb.bots_ready.Wait(io) else {}
        return
    }
    defer conn.Close()
    var arena := Arena.New(hb.heap) else {
        _ = hb.bots_ready.Wait(io) else {}
        return
    }
    defer arena.Free()
    var rbuf: [2048]u8 = [0; 2048]
    var r := conn.Reader(io, mut rbuf[..])
    var line: [600]u8 = [0; 600]
    nick := arena.Fmt("NICK {n}", (n: BotNames[i % 2])) else {
        _ = hb.bots_ready.Wait(io) else {}
        return
    }
    ready := Await(mut r, mut line[..]) && Ask(&conn, io, mut r, mut line[..], nick) && Ask(&conn, io, mut r, mut line[..], "JOIN #bots")
    _ = hb.bots_ready.Wait(io) else {
        return
    }
    if !ready {
        return
    }
    for {
        got := ReadLine(mut r, mut line[..]) else {
            return
        }
        l := got else {
            return
        }
        reply := Answer(&arena, hb, i, l) else {
            return
        }
        if s := reply {
            SendLine(&conn, io, s) else {
                return
            }
        }
    }
}

/// What bot `i` says to `line`, if anything.
Answer(a: &Arena, hb: &hub.Hub, i: int, line: str): OutOfMemory!?str from a, line {
    if i % 2 == 0 {
        at := line.Find(": !echo ") else {
            return none
        }
        return some(try a.Fmt("MSG #bots {t}", (t: line[at + 8..])))
    }
    if !line.Has(": !stats") {
        return none
    }
    return some(try a.Fmt("MSG #bots online={o} messages={m}", (o: hb.stats.online.Load(atomic.Acquire), m: hb.stats.messages.Load())))
}

/// Reads up to the next `OK` or `ERR` line; true when it was `OK`.
export Await(r: mut Reader, buf: mut []u8): bool {
    for {
        got := ReadLine(r, buf) else {
            return false
        }
        l := got else {
            return false
        }
        if Final(l) {
            return l.StartsWith("OK")
        }
    }
}

/// Sends `cmd` and reads to the reply; true when it was `OK`.
export Ask(conn: &net.Stream, io: Io, r: mut Reader, buf: mut []u8, cmd: str): bool {
    SendLine(conn, io, cmd) else {
        return false
    }
    return Await(r, buf)
}
