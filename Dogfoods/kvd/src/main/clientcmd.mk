// Why: the command-line client a user types kvd commands into.
// What: `kvd client`: RunClient and Exchange, one command from the
// arguments, or one per line of standard input.
// How: each command goes through client.Call and its reply is printed.

import std.net
import src.client

/// The flags `kvd client` takes.
const ClientFlags: Accepts = Accepts(values: ["--addr"], switches: [])

/// Joins `words` with single spaces, in `a`.
Join(a: &Arena, words: []str): OutOfMemory!str from a {
    var t := Text.New(a)
    for i, word in words {
        if i > 0 {
            try t.Push(" ")
        }
        try t.Push(word)
    }
    return t.Done()
}

/// Sends `request` and writes its reply lines to `out`, each after the
/// request when `echo`. Returns whether the reply was an error.
Exchange(a: &Arena, r: mut Reader, w: mut Writer, request: str, out: mut Writer, echo: bool): client.Error!bool {
    lines := try client.Call(a, r, w, request)
    if echo {
        try Print(out, "> {request}\n", (request))
    }
    for line in lines {
        try Print(out, "{line}\n", (line))
    }
    return lines[0].StartsWith("-")
}

/// `kvd client`: exits 1 when the server cannot be reached or a reply is an
/// error, 0 otherwise.
RunClient(sys: &System, rest: []str): int *rest into sys {
    io := sys.io
    args := ParseArgs(sys.arena, rest, &ClientFlags) else e {
        return UsageError(sys, e)
    }
    addr := args.Str("--addr", "127.0.0.1:7379")
    target := client.Lookup(sys.arena, io, addr) else e {
        sys.PrintErr("kvd: address {addr}: {e}\n", (addr, e)) else {}
        return ExitFailure
    }
    conn := client.Connect(io, target, 1) else e {
        match e {
            net.Os(code) {
                sys.PrintErr("kvd: connect to {addr}: system error {code}\n", (addr, code)) else {}
            }
            _ {
                sys.PrintErr("kvd: connect to {addr}: {e}\n", (addr, e)) else {}
            }
        }
        return ExitFailure
    }
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    var out := sys.Stdout()
    var failed := false
    if args.plain.Len() > 0 {
        request := Join(sys.arena, args.plain.Items()) else {
            return ExitMemory
        }
        failed = Exchange(sys.arena, mut r, mut w, request, mut out, false) else e {
            sys.PrintErr("kvd: {e}\n", (e)) else {}
            return ExitFailure
        }
        return if failed { ExitFailure } else { 0 }
    }
    var input := sys.Stdin()
    var scratch := Arena.New(sys.heap) else {
        return ExitMemory
    }
    defer scratch.Free()
    for {
        got := input.Line(&scratch) else e {
            sys.PrintErr("kvd: reading commands: {e}\n", (e)) else {}
            return ExitFailure
        }
        line := got else {
            break
        }
        if line.Trim().Len() == 0 {
            continue
        }
        bad := Exchange(&scratch, mut r, mut w, line, mut out, true) else e {
            sys.PrintErr("kvd: {e}\n", (e)) else {}
            return ExitFailure
        }
        failed = failed || bad
        scratch.Reset()
    }
    return if failed { ExitFailure } else { 0 }
}
