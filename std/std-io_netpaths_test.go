// Why: std.net must reject bad addresses and release every handle on every
// path, failing or not, against the real OS.
// What: TestSTDIO_NetInvalidAddress and TestSTDIO_NetPathsReleaseHandles.
// How: every malformed address must be InvalidAddress from Dial and Listen;
// every path (AddrInUse, Refused, a Dial and an Accept in a cancelled scope,
// a full connect, send, recv and close) must leave sys.HandleCount where it
// was. Each test has a Linux run in TestStdIo_LinuxRuns.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// m5netHandles is the fixture net/zhandles.mk: the OS's open-handle count
// for a program, which cannot import std.sys.
const m5netHandles = `import std.sys

export Handles(): int {
    return sys.HandleCount()
}
`

// m5netProgram: args[1] a mode. "addr": Dial and Listen each malformed
// address of Bad and print one line per call, "<i> dial <name>" and
// "<i> listen <name>". "leak": args[2] rounds, args[3] how many of them
// also dial a closed port (Refused costs about a second on Windows, which
// retries a refused SYN); prints "handles H0 H1 bad B", B the calls whose
// result was not the one expected. "refused": args[2] dials to a closed
// port, then "refused N ms M".
const m5netProgram = `import std.net
import std.time

const Bad: [10]str = ["127.0.0.1:65536", "127.0.0.1", ":", "", "[::1]:99999", "127.0.0.1:-1",
    "bad name:80", "127.0.0.1:8x", "[::1", "127.0.0.1:99999999999999999999"]

Name(e: net.Error): str {
    match e {
        net.Refused {
            return "Refused"
        }
        net.Reset {
            return "Reset"
        }
        net.AddrInUse {
            return "AddrInUse"
        }
        net.Unreachable {
            return "Unreachable"
        }
        net.InvalidAddress {
            return "InvalidAddress"
        }
        net.Os(code: _) {
            return "Os"
        }
        Canceled {
            return "Canceled"
        }
        Closed {
            return "Closed"
        }
        _ {
            // 1.2's members: time.TimedOut, net.Unsupported,
            // net.SecureFailed; net.Error may gain more.
            return "other"
        }
    }
}

// Addr writes "127.0.0.1:<port>" into b.
Addr(b: mut [32]u8, port: int): str from b {
    pre := "127.0.0.1:".AsBytes()
    for i in 0..pre.Len() {
        b[i] = pre[i]
    }
    var d: [8]u8 = [0; 8]
    var n := 0
    var x := port
    for {
        d[n] = Trunc[u8](48 + x % 10)
        n += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..n {
        b[pre.Len() + i] = d[n - 1 - i]
    }
    return str.From(b[0..pre.Len() + n]).Must()
}

// Is is 0 when the call failed with want, else 1 (and a success is closed).
DialIs(io: Io, a: str, want: str): int {
    s := net.Dial(io, a) else e {
        if Name(e) == want {
            return 0
        }
        return 1
    }
    s.Close()
    return 1
}

ListenIs(io: Io, a: str, want: str): int {
    l := net.Listen(io, a) else e {
        if Name(e) == want {
            return 0
        }
        return 1
    }
    l.Close()
    return 1
}

AcceptIs(l: &net.Listener, io: Io, want: str): int {
    s := l.Accept(io) else e {
        if Name(e) == want {
            return 0
        }
        return 1
    }
    s.Close()
    return 1
}

// Round is one pass over every path; the count of unexpected results.
Round(io: Io, l: &net.Listener, la: str, ca: str, refused: bool, round: int): int {
    var bad := 0
    bad += DialIs(io, Bad[round % 10], "InvalidAddress")
    bad += ListenIs(io, Bad[(round + 3) % 10], "InvalidAddress")
    bad += ListenIs(io, la, "AddrInUse")
    if refused {
        bad += DialIs(io, ca, "Refused")
    }
    scope s := io.Scope() {
        s.Cancel()
        bad += DialIs(io, la, "Canceled")
        bad += AcceptIs(l, io, "Canceled")
    }
    c := net.Dial(io, la) else {
        return bad + 1
    }
    a := l.Accept(io) else {
        c.Close()
        return bad + 1
    }
    var b: [1]u8 = [0x5a]
    c.WriteAll(io, b[..]) else {
        bad += 1
    }
    var r: [4]u8 = [0; 4]
    n := a.Read(io, mut r[..]) else {
        a.Close()
        c.Close()
        return bad + 1
    }
    if n != 1 || r[0] != 0x5a {
        bad += 1
    }
    a.Close()
    // The peer closed: a read is end of stream (0), never an error.
    m := c.Read(io, mut r[..]) else {
        c.Close()
        return bad + 1
    }
    if m != 0 {
        bad += 1
    }
    c.Close()
    return bad
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    io := sys.io
    mode := sys.args[1]
    if mode == "addr" {
        for i in 0..10 {
            s := net.Dial(io, Bad[i]) else e {
                name := Name(e)
                try Print(mut out, "{i} dial {name}\n", (i, name))
                continue
            }
            s.Close()
            try Print(mut out, "{i} dial ok\n", (i))
        }
        for i in 0..10 {
            l := net.Listen(io, Bad[i]) else e {
                name := Name(e)
                try Print(mut out, "{i} listen {name}\n", (i, name))
                continue
            }
            l.Close()
            try Print(mut out, "{i} listen ok\n", (i))
        }
        return
    }
    l := net.Listen(io, "127.0.0.1:0") else e {
        name := Name(e)
        try Print(mut out, "listen {name}\n", (name))
        return
    }
    var lb: [32]u8 = [0; 32]
    la := Addr(mut lb, l.Port())
    gone := net.Listen(io, "127.0.0.1:0") else e {
        l.Close()
        name := Name(e)
        try Print(mut out, "listen {name}\n", (name))
        return
    }
    var cb: [32]u8 = [0; 32]
    ca := Addr(mut cb, gone.Port())
    gone.Close()
    if mode == "refused" {
        n := int(sys.args[2].ParseInt() else {
            l.Close()
            return
        })
        t0 := time.Now(io)
        var bad := 0
        for _ in 0..n {
            bad += DialIs(io, ca, "Refused")
        }
        ms := time.Now(io).Since(t0).Nanos() / 1_000_000
        l.Close()
        try Print(mut out, "refused {n} bad {bad} ms {ms}\n", (n, bad, ms))
        return
    }
    rounds := int(sys.args[2].ParseInt() else {
        l.Close()
        return
    })
    refused := int(sys.args[3].ParseInt() else {
        l.Close()
        return
    })
    // Two warm-up rounds, so lazily opened runtime handles (the poller,
    // the blocking pool, the socket library) are counted in both counts.
    var bad := Round(io, &l, la, ca, true, 0)
    bad += Round(io, &l, la, ca, false, 1)
    h0 := net.Handles()
    for i in 0..rounds {
        bad += Round(io, &l, la, ca, i < refused, i)
    }
    h1 := net.Handles()
    l.Close()
    try Print(mut out, "handles {h0} {h1} bad {bad}\n", (h0, h1, bad))
}
`

func buildM5Net(t *testing.T, h ioHost) string {
	return h.build(t, map[string]string{"net/zhandles.mk": m5netHandles}, m5netProgram)
}

// TestSTDIO_NetInvalidAddress: each malformed address (a port above
// 65535 or far past i64, no port, an empty host and port, an empty
// string, a bad IPv6 literal, a negative or non-numeric port, a space in
// the host) is InvalidAddress from Dial and from Listen, on both targets,
// with no name lookup (none of them is a valid host name).
func TestSTDIO_NetInvalidAddress(t *testing.T) { ioNetInvalidAddress(t, hostIo) }

func ioNetInvalidAddress(t *testing.T, h ioHost) {
	exe := buildM5Net(t, h)
	r := h.runMany(t, exe, []ioCall{{args: []string{"addr"}, watch: 60}})[0]
	var want strings.Builder
	for _, op := range []string{"dial", "listen"} {
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&want, "%d %s InvalidAddress\n", i, op)
		}
	}
	if got := strings.ReplaceAll(r.stdout, "\r\n", "\n"); r.code != 0 || got != want.String() {
		t.Errorf("exit %d\n%s\nwant\n%s%s", r.code, got, want.String(), faultTail(r))
	}
}

// TestSTDIO_NetPathsReleaseHandles: 200 rounds of every std.net path
// (InvalidAddress from Dial and Listen, AddrInUse, a Dial and an Accept in
// a cancelled scope, a connect with one byte each way, a read at end of
// stream; 10 of the rounds also a Refused dial) leave the OS's handle
// count where it was, and each call gives the result expected.
func TestSTDIO_NetPathsReleaseHandles(t *testing.T) { ioNetPathsReleaseHandles(t, hostIo) }

func ioNetPathsReleaseHandles(t *testing.T, h ioHost) {
	exe := buildM5Net(t, h)
	// The default carriers (one per CPU), then one carrier:
	// there the dialing task and the accepting task share one thread, so
	// every path must park and resume through the poller, never block it.
	calls := []ioCall{
		{args: []string{"leak", "200", "10"}, watch: 240},
		{args: []string{"leak", "100", "3"}, env: []string{"MK_CARRIERS=1"}, watch: 240},
	}
	for i, r := range h.runMany(t, exe, calls) {
		what := "default carriers"
		if i == 1 {
			what = "MK_CARRIERS=1"
		}
		var h0, h1, bad int
		if _, err := fmt.Sscanf(r.stdout, "handles %d %d bad %d", &h0, &h1, &bad); err != nil || r.code != 0 {
			t.Errorf("%s: exit %d, %q (%v)%s", what, r.code, r.stdout, err, faultTail(r))
			continue
		}
		if bad != 0 {
			t.Errorf("%s: %d calls gave another result than expected", what, bad)
		}
		// A path that leaks one handle per round leaves one per round
		// (one per Refused round for Refused).
		if h0 <= 0 || h1-h0 > 4 || h1 < h0-4 {
			t.Errorf("%s: open handles %d before the rounds, %d after: a path leaks", what, h0, h1)
		}
		t.Logf("%s: open handles: %d before, %d after", what, h0, h1)
	}
}
