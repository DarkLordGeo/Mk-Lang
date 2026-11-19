// Why: every byte std.websocket reads comes from an untrusted peer; no input
// may panic, hang or grow memory past the limits.
// What: FuzzStdWs_Frames, FuzzStdWs_Reply, FuzzStdWs_Request and
// TestStdWs_FuzzReplySeedsConnect.
// How: one release program per input over std.sim's loopback network.

// The fuzz targets for std.websocket: the bytes a peer sends after the
// handshake go through Conn.Recv, as frames from a server to a Dial'ed
// client (unmasked side) and from a client to an Accept'ed server (masked
// side), the bytes a server sends as its reply head go through Dial, and
// the bytes a client sends as its request go through Accept (its offer of
// subprotocols through Choose).
// The program, built once with --release, runs one input per process over
// std.sim's loopback network (no OS socket, so a fuzz run leaves no
// TIME_WAIT entry): it must never panic or hang, and prints how many
// messages it received and how the connection ended, exiting 0. The
// limits are 64 KiB a frame and 128 KiB a message, so memory stays
// bounded by them whatever the input announces. The seeds are RFC 6455
// 5.7's frames, fragmented and interleaved messages, each violation of
// tests_conn.mk, and RFC 6455 1.3's reply with its variants. go test runs
// the seeds; go test -fuzz FuzzStdWs_Frames -parallel 2 explores (at most
// 5 minutes a target per run).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// wsFuzzProgram reads its input from standard input; its argument picks
// the target: client (frames to a client), server (frames to a server),
// reply (a reply head to Dial) or request (a request head to Accept, which
// chooses among superchat and chat).
const wsFuzzProgram = `import std.encoding
import std.hash
import std.http
import std.net
import std.sim
import std.time
import std.url
import std.websocket

const Addr = "127.0.0.1:7600"

export Main(sys: &System): websocket.Error|url.Error|http.Error|ReadError|WriteError!void {
    var inp := sys.Stdin()
    buf := try sys.arena.Slice[u8](70000)
    n := try inp.ReadAll(buf)
    data := buf[0..n]
    mode := sys.args[1]
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    l := try net.Listen(io, Addr)
    defer l.Close()
    var o := websocket.DefaultOptions
    o.frame_bytes = 65536
    o.message_bytes = 131072
    offered: [2]str = ["chat", "superchat"]
    if mode == "reply" {
        o.protocols = offered[..]
    }
    scope s := io.Scope() {
        if mode == "server" || mode == "request" {
            s.go ClientPeer(io, data, mode == "request")
            try Server(sys, io, &l)
        } else {
            s.go ServerPeer(io, &l, data, mode == "reply")
            u := try url.Parse(sys.arena, "ws://127.0.0.1:7600/fuzz", none)
            c := websocket.Dial(sys.heap, sys.arena, io, &u, &o) else e {
                try sys.Print("dial {e}\n", (e))
                return
            }
            p := if q := c.Protocol() { q } else { "-" }
            sys.Print("protocol {p}\n", (p: p)) else e {
                c.Abandon()
                fail e
            }
            Drain(sys, io, c)
        }
    }
}

// Drain receives until the connection ends, each message in an arena of
// its own, then prints the count and the error.
Drain(sys: &System, io: Io, c: websocket.Conn) {
    var count := 0
    for {
        var a := Arena.New(sys.heap) else {
            c.Abandon()
            return
        }
        m := c.Recv(&a, io) else e {
            a.Free()
            c.Abandon()
            sys.Print("{count} messages, {e}\n", (count, e)) else {}
            return
        }
        _ = m.data.Len()
        a.Free()
        count += 1
    }
}

// ServerPeer answers the handshake on l: with data as the reply head when
// reply is true, its first {accept} replaced by the accept value of the
// key Dial sent (WS-7), else with a correct 101 followed by data; then it
// closes.
ServerPeer(io: Io, l: &net.Listener, data: []u8, reply: bool) {
    st := l.Accept(io) else {
        return
    }
    c := st.Conn()
    var head: [4096]u8 = [0; 4096]
    var n := 0
    var key: [24]u8 = [0; 24]
    for n < head.Len() {
        k := c.Read(io, mut head[n..]) else {
            c.Abandon()
            return
        }
        if k == 0 {
            break
        }
        n += k
        if n >= 4 && head[n - 1] == 0x0A && head[n - 2] == 0x0D && head[n - 3] == 0x0A {
            break
        }
    }
    tag := "Sec-WebSocket-Key: ".AsBytes()
    var i := 0
    for i + tag.Len() + 24 <= n {
        var same := true
        for j in 0..tag.Len() {
            if head[i + j] != tag[j] {
                same = false
                break
            }
        }
        if same {
            (mut key[..]).CopyFrom(head[i + tag.Len()..i + tag.Len() + 24])
        }
        i += 1
    }
    var acc: [28]u8 = [0; 28]
    var h := hash.Sha1State.New()
    h.Bytes(key[..])
    h.Bytes("258EAFA5-E914-47DA-95CA-C5AB0DC85B11".AsBytes())
    sum := h.Sum()
    _ = encoding.Base64EncodeTo(mut acc[..], sum[..], encoding.Base64)
    if reply {
        WriteReply(&c, io, data, acc[..])
    } else {
        c.WriteAll(io, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ".AsBytes()) else {}
        c.WriteAll(io, acc[..]) else {}
        c.WriteAll(io, "\r\n\r\n".AsBytes()) else {}
        c.WriteAll(io, data) else {}
    }
    c.Abandon()
}

// WriteReply writes data to c with its first {accept} replaced by acc.
WriteReply(c: &net.Conn, io: Io, data: []u8, acc: []u8) {
    tag := "{accept}".AsBytes()
    var i := 0
    for i + tag.Len() <= data.Len() {
        var same := true
        for j in 0..tag.Len() {
            if data[i + j] != tag[j] {
                same = false
                break
            }
        }
        if same {
            c.WriteAll(io, data[0..i]) else {
                return
            }
            c.WriteAll(io, acc) else {
                return
            }
            c.WriteAll(io, data[i + tag.Len()..]) else {}
            return
        }
        i += 1
    }
    c.WriteAll(io, data) else {}
}

// ClientPeer dials Addr, sends RFC 6455 1.3's request (data is the request
// itself when request is true), then data, and closes.
ClientPeer(io: Io, data: []u8, request: bool) {
    st := net.Dial(io, Addr) else {
        return
    }
    c := st.Conn()
    if !request {
        c.WriteAll(io, "GET /chat HTTP/1.1\r\nHost: server.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n".AsBytes()) else {}
        // The server reads the request alone, then the frames.
        io.Sleep(time.Millis(1)) else {}
    }
    c.WriteAll(io, data) else {}
    var tail: [256]u8 = [0; 256]
    for {
        k := c.Read(io, mut tail[..]) else {
            break
        }
        if k == 0 {
            break
        }
    }
    c.Abandon()
}

AllowAll(origin: str): bool {
    return true
}

// Server accepts one connection on l, reads its request, accepts it
// supporting superchat and chat, prints the subprotocol chosen, and
// receives until the connection ends. A request std.http refuses is
// printed as "head" and is no finding of std.websocket's.
Server(sys: &System, io: Io, l: &net.Listener): websocket.Error|http.Error|WriteError!void {
    // The request lives in an arena of its own: ReadRequest's arena
    // stores its own loan.
    var ra := try Arena.New(sys.heap)
    defer ra.Free()
    st := try l.Accept(io)
    req := Head(&ra, io, &st) else e {
        st.Close()
        try sys.Print("head {e}\n", (e))
        return
    }
    t := http.Taken(conn: st.Conn(), rest: "".AsBytes())
    supported: [2]str = ["superchat", "chat"]
    c := websocket.Accept(sys.heap, io, &req, t, supported[..], AllowAll) else e {
        t.Abandon()
        try sys.Print("accept {e}\n", (e))
        return
    }
    p := if q := c.Protocol() { q } else { "-" }
    sys.Print("protocol {p}\n", (p: p)) else e {
        c.Abandon()
        fail e
    }
    Drain(sys, io, c)
}

// Head reads one request head from s into a (the request's
// fields are views of its head in a, so a stores its own loan).
Head(a: &Arena into a, io: Io, s: &net.Stream): http.Error!http.Request from a {
    var rb: [4096]u8 = [0; 4096]
    var r := s.Reader(io, mut rb[..])
    return try http.ReadRequest(a, io, mut r, 16384)
}
`

// wsFuzzSeeds are frames as either side receives them; wsMaskAll masks a
// seed's frames for the server target.
var wsFuzzSeeds = [][]byte{
	{0x81, 0x05, 'H', 'e', 'l', 'l', 'o'},
	{0x01, 0x03, 'H', 'e', 'l', 0x89, 0x05, 'H', 'e', 'l', 'l', 'o', 0x80, 0x02, 'l', 'o'},
	{0x82, 0x7E, 0x01, 0x00},
	{0x82, 0x7F, 0, 0, 0, 0, 0, 1, 0, 0},
	{0x82, 0x7F, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	{0xC1, 0x00},
	{0x88, 0x02, 0x03, 0xE8},
	{0x88, 0x01, 0x03},
	{0x81, 0x02, 0xC0, 0x80},
	{0x8A, 0x00, 0x89, 0x7E, 0x00, 0x7E},
	{0x00, 0x00},
	{0x01, 0x01, 'a', 0x01, 0x01, 'b'},
}

// wsMasked is seed with every frame masked under key 1 2 3 4, as a client
// sends it (header lengths kept as they are).
func wsMasked(seed []byte) []byte {
	var out []byte
	for i := 0; i < len(seed); {
		if i+2 > len(seed) {
			return append(out, seed[i:]...)
		}
		b0, b1 := seed[i], seed[i+1]
		n := int(b1 & 0x7F)
		hl := 2
		switch n {
		case 126:
			hl = 4
		case 127:
			hl = 10
		}
		if i+hl > len(seed) {
			return append(out, seed[i:]...)
		}
		switch n {
		case 126:
			n = int(seed[i+2])<<8 | int(seed[i+3])
		case 127:
			n = 1 << 30
		}
		out = append(out, b0, b1|0x80)
		out = append(out, seed[i+2:i+hl]...)
		out = append(out, 1, 2, 3, 4)
		end := i + hl + n
		if end > len(seed) || end < 0 {
			end = len(seed)
		}
		for j, b := range seed[i+hl : end] {
			out = append(out, b^byte(j%4+1))
		}
		i = end
	}
	return out
}

// wsFuzzExe builds wsFuzzProgram once per target, into the target's own
// directory: a seed's TempDir is removed when that seed ends.
func wsFuzzExe(f *testing.F) func(t *testing.T) string {
	dir := f.TempDir()
	var once sync.Once
	var exe string
	return func(t *testing.T) string {
		once.Do(func() {
			built := buildNative(t, nil, wsFuzzProgram, "--release")
			b, err := os.ReadFile(built)
			if err != nil {
				t.Fatal(err)
			}
			exe = filepath.Join(dir, filepath.Base(built))
			if err := os.WriteFile(exe, b, 0o755); err != nil {
				t.Fatal(err)
			}
		})
		if exe == "" {
			// A build that failed (a std mid-edit) is no finding.
			t.Skip("the fuzz program did not build")
		}
		return exe
	}
}

// wsFuzzRun runs the program on input as mode and fails t unless it ended
// with a count and an error, or a dial or accept error, exiting 0.
func wsFuzzRun(t *testing.T, exe, mode string, input []byte) {
	if len(input) > 65536 {
		input = input[:65536]
	}
	r := runNative(t, exe, string(input), mode)
	out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
	if r.code != 0 || !(strings.Contains(out, " messages, ") || strings.HasPrefix(out, "dial ") || strings.HasPrefix(out, "accept ") || strings.HasPrefix(out, "head ")) {
		t.Fatalf("%s: exit %d on %q\n%s%s", mode, r.code, input, r.stdout, r.stderr)
	}
}

func FuzzStdWs_Frames(f *testing.F) {
	for _, s := range wsFuzzSeeds {
		f.Add(false, s)
		f.Add(true, wsMasked(s))
	}
	exe := wsFuzzExe(f)
	f.Fuzz(func(t *testing.T, server bool, frames []byte) {
		mode := "client"
		if server {
			mode = "server"
		}
		wsFuzzRun(t, exe(t), mode, frames)
	})
}

// wsReplySeeds are FuzzStdWs_Reply's seeds that reach a working
// connection: RFC 6455 1.3's reply with {accept} for the accept value of
// the key Dial sent (the program puts it in), with a subprotocol Dial
// offered, and with frames after the head in the same read; and
// one naming a subprotocol Dial did not offer. Each with what its run
// must print.
var wsReplySeeds = []struct{ reply, want string }{
	{"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n\r\n", "protocol -\n0 messages, PeerClosed"},
	{"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\nSec-WebSocket-Protocol: chat\r\n\r\n\x81\x02hi", "protocol chat\n1 messages, PeerClosed"},
	{"HTTP/1.1 101 OK\r\nupgrade: WebSocket\r\nconnection: keep-alive, Upgrade\r\nsec-websocket-protocol: superchat\r\nSec-WebSocket-Accept: {accept}\r\n\r\n\x01\x01a\x80\x01b\x82\x7e\x00\x80" + strings.Repeat("z", 128) + "\x88\x02\x03\xe8", "protocol superchat\n2 messages, PeerClosed(code: 1000)"},
	{"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\nSec-WebSocket-Protocol: mqtt\r\n\r\n", "dial Handshake(status: 101)"},
}

func FuzzStdWs_Reply(f *testing.F) {
	for _, s := range wsReplySeeds {
		f.Add([]byte(s.reply))
	}
	for _, s := range []string{
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n",
		"HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: x\r\nSec-WebSocket-Protocol: chat\r\n\r\n\x81\x02hi",
		"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n",
		"HTTP/1.1 101 OK\nUpgrade: websocket\n\n",
		"HTTP/1.1 101 OK\r\n Upgrade: websocket\r\n\r\n",
		"HTTP/1.0 101 OK\r\n\r\n",
		"HTTP/1.1 101 OK\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n",
		"HTTP/1.1 101",
	} {
		f.Add([]byte(s))
	}
	exe := wsFuzzExe(f)
	f.Fuzz(func(t *testing.T, reply []byte) {
		wsFuzzRun(t, exe(t), "reply", reply)
	})
}

// FuzzStdWs_Request feeds a client's request head to Accept: the offer of
// subprotocols is untrusted bytes read by Choose. Bytes past the
// head go into Head's 4,096-byte reader and are dropped (the Taken's rest
// is empty), so frames after it reach Recv only past that buffer; the
// frame targets cover Recv. The seeds are RFC 6455 1.3's request and
// variants of its offer.
func FuzzStdWs_Request(f *testing.F) {
	const base = "GET /chat HTTP/1.1\r\nHost: server.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
	for _, s := range []string{
		base + "\r\n",
		base + "Sec-WebSocket-Protocol: chat, superchat\r\n\r\n",
		base + "Sec-WebSocket-Protocol: v1, chat\r\nSec-WebSocket-Protocol: superchat\r\n\r\n",
		base + "Sec-WebSocket-Protocol: ,, \t ,chat;x, Chat, super chat\r\n\r\n",
		base + "Sec-WebSocket-Protocol: " + strings.Repeat("a", 20000) + ", chat\r\n\r\n",
		base + "Sec-WebSocket-Protocol: chat\r\n\r\n\x81\x85\x37\xfa\x21\x3d\x7f\x9f\x4d\x51\x58",
		base + "Sec-WebSocket-Version: 8\r\nSec-WebSocket-Protocol: chat\r\n\r\n",
	} {
		f.Add([]byte(s))
	}
	exe := wsFuzzExe(f)
	f.Fuzz(func(t *testing.T, request []byte) {
		wsFuzzRun(t, exe(t), "request", request)
	})
}

// TestStdWs_FuzzReplySeedsConnect: FuzzStdWs_Reply's seeds with
// {accept} pass Dial's check of the accept value, so the fuzzer starts
// from the accepted-reply path (the subprotocol copy, the head consumed,
// the frames after it read), and each prints what it must.
func TestStdWs_FuzzReplySeedsConnect(t *testing.T) {
	exe := buildNative(t, nil, wsFuzzProgram, "--release")
	for _, s := range wsReplySeeds {
		r := runNative(t, exe, s.reply, "reply")
		out := strings.ReplaceAll(r.stdout, "\r\n", "\n")
		if r.code != 0 || !strings.Contains(out, s.want) {
			t.Errorf("reply %q: exit %d, printed %q, want %q\n%s", s.reply, r.code, out, s.want, r.stderr)
		}
	}
}
