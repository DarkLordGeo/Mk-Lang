// Why: an operator talks to a running server over a channel that clients
// cannot reach.
// What: `kvd admin`: RunAdmin and AdminCall, one command over the admin
// channel, a Unix-domain socket named by `kvd serve --admin PATH`.
// How: connects with client.ConnectAdmin, sends one line, prints the reply
// and exits 1 when the reply is an error.

import std.time
import src.client

/// The flags `kvd admin` takes.
const AdminFlags: Accepts = Accepts(values: ["--admin", "--timeout-ms"], switches: [])

/// Sends `request` over the admin channel at `path` and writes the reply
/// to `out`. Returns whether the reply was an error.
AdminCall(a: &Arena, io: Io, path: str, request: str, out: mut Writer): client.Error!bool {
    conn := try client.ConnectAdmin(io, path)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    return try Exchange(a, mut r, mut w, request, out, false)
}

/// `kvd admin --admin PATH COMMAND...`: exits 1 when the channel cannot be
/// reached, the reply is an error, or no reply comes within `--timeout-ms`
/// (5,000 by default; a Unix stream on Windows may never see its peer
/// leave, so the wait is bounded).
RunAdmin(sys: &System, rest: []str): int *rest into sys {
    io := sys.io
    args := ParseArgs(sys.arena, rest, &AdminFlags) else e {
        return UsageError(sys, e)
    }
    limit := args.Int("--timeout-ms", 1, 5000) else e {
        return UsageError(sys, e)
    }
    path := args.Get("--admin") else {
        sys.PrintErr("kvd: admin needs --admin PATH; see kvd --help\n") else {}
        return ExitUsage
    }
    if args.plain.Len() == 0 {
        sys.PrintErr("kvd: admin needs a command; see kvd --help\n") else {}
        return ExitUsage
    }
    request := Join(sys.arena, args.plain.Items()) else {
        return ExitMemory
    }
    var out := sys.Stdout()
    var code := 0
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(limit))
        failed := AdminCall(sys.arena, io, path, request, mut out) else e {
            if s.Expired() {
                sys.PrintErr("kvd: admin channel {path}: no reply within {limit} ms\n", (path, limit)) else {}
            } else {
                sys.PrintErr("kvd: admin channel {path}: {e}\n", (path, e)) else {}
            }
            true
        }
        if failed {
            code = ExitFailure
        }
    }
    return code
}
