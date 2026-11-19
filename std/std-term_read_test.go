// Why: Term.Read wraps the decoder in buffering, paste and Windows record
// logic that the decoder's fuzz target never reaches.
// What: FuzzTerm_Read, TestTerm_ReadRecorded, TestTerm_ShortWrites and the
// Linux fuzz leg termLinuxFuzz.
// How: drives Read with no terminal in chunks and compares the events with
// termModel over the whole stream; unicode/utf16 judges the records.

// The fuzz target of
// Term.Read's state machine and the short-write check of
// Term.Close. FuzzTerm_Parse (std-term_decode_test.go) reaches
// only the decoder; this target drives what Read does around it with no
// terminal: the Windows record path (Record, Unit, PutScalar, sized by
// RecordsFor), the buffer's compaction and Fill's guard, the paste path
// (PasteStep, FindFrom's offset, the cut of a full buffer), the skip state
// and the escape held to the end. The oracle is Go's unicode/utf16 for the
// records (the Encoding Standard's UTF-16 decoder) and termModel for the
// events, run over the whole stream at once, so a chunk boundary that
// changes an event, a paste that loses or gains a byte, or a read that
// writes past the buffer fails.
//
//	go test ./std -run 'TestTerm_ReadRecorded|TestTerm_ShortWrites'
//	go test ./std -run '^$' -fuzz FuzzTerm_Read -fuzztime 5m -parallel 2
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// termReadHook is the extra std/term file. ZzRead takes a script: a flags
// byte (bit 0: a paste is under way; bit 1: a high surrogate is left from
// an earlier read), a starting tail (two bytes, little-endian, taken modulo
// InBytes - Room + 1, the held bytes being 'x'), then UTF-16 units (two
// bytes each, little-endian), each a key-down record. Each round is one
// Fill: compaction, Fill's guard, then as many records as RecordsFor allows
// (at most Records), then Decode until it needs input. At the end of the
// script, Decode with final until nothing more is held, as Read's escape
// wait does. It writes one line per event ("K key f ch shift alt ctrl", "P
// <hex>" for a paste's last part and "Q <hex>" for a piece before it, "M
// button row col shift alt ctrl", "F on"), then "T pasting skip", then
// "END"; "STUCK" when Fill's guard would fail or the end does not
// converge.
const termReadHook = `export ZzRead(w: mut Writer, a: &Arena, b: []u8): WriteError|OutOfMemory!void {
    if b.Len() < 3 {
        try w.Str("END\n")
        return
    }
    start := (int(b[1]) | (int(b[2]) << 8)) % (InBytes - Room + 1)
    pasting := b[0] & 1 != 0
    high: u32 = if b[0] & 2 != 0 { 0xD800 } else { 0 }
    var t := Term(input: 0, out: 0, is_raw: false, saved_len: 0, saved: [0; 64], alt: false, alt_at: 0,
        in_buf: [0; 4096], head: 0, tail: start, holding: false, held_at: 0, pasting: pasting, high: high, resized: false, empties: 0, cols: 0,
        rows: 0, reg: -1, mouse: false, mouse_at: 0, skip: 0, scan: 0, secret: [0; 1024], secret_len: 0,
        secret_raw: false, kitty: KittyZero())
    for i in 0..start {
        t.in_buf[i] = b'x'
    }
    var i := 3
    var stuck := false
    for i + 1 < b.Len() && !stuck {
        t.Compact()
        if InBytes - t.tail < Room {
            stuck = true
            break
        }
        room := RecordsFor(InBytes - t.tail, t.high)
        count := if room < Records { room } else { Records }
        var k := 0
        for k < count && i + 1 < b.Len() {
            var r: [20]u8 = [0; 20]
            r[0] = 1
            r[4] = 1
            r[14] = b[i]
            r[15] = b[i + 1]
            t.Record(r[..])
            Assert(t.tail <= InBytes)
            i += 2
            k += 1
        }
        for {
            if e := t.Decode(a, false, false).Must() {
                ZzEvent(w, e, t.pasting).Must()
            } else {
                break
            }
        }
    }
    var rounds := 0
    for !stuck {
        if e := t.Decode(a, false, true).Must() {
            ZzEvent(w, e, t.pasting).Must()
        } else if !(t.head < t.tail && !t.pasting) {
            break
        }
        rounds += 1
        if rounds > 2 * InBytes {
            stuck = true
        }
    }
    p := if t.pasting { 1 } else { 0 }
    s := t.skip
    Term(input: _, out: _, is_raw: _, saved_len: _, saved: _, alt: _, alt_at: _, in_buf: _, head: _, tail: _, holding: _, held_at: _, pasting: _,
        high: _, resized: _, empties: _, cols: _, rows: _, reg: _, mouse: _, mouse_at: _, skip: _, scan: _, secret: _, secret_len: _,
        secret_raw: _, kitty: _) := t
    if stuck {
        try w.Str("STUCK\n")
    }
    try Print(w, "T {p} {s}\nEND\n", (p, s))
}

// ZzEvent writes e; a Paste is "Q" while the paste goes on (a piece a full
// buffer cut), "P" for its last part.
ZzEvent(w: mut Writer, e: Event, open: bool): WriteError!void {
    match e {
        .Key(key, ch, mods) {
            var f := 0
            k := match key {
                .Char { 0 }
                .Enter { 1 }
                .Escape { 2 }
                .Backspace { 3 }
                .Tab { 4 }
                .Up { 5 }
                .Down { 6 }
                .Left { 7 }
                .Right { 8 }
                .Home { 9 }
                .End { 10 }
                .PageUp { 11 }
                .PageDown { 12 }
                .Insert { 13 }
                .Delete { 14 }
                .F(n) {
                    f = n
                    15
                }
            }
            c := u32(ch)
            s := if mods.shift { 1 } else { 0 }
            al := if mods.alt { 1 } else { 0 }
            ct := if mods.ctrl { 1 } else { 0 }
            try Print(w, "K {k} {f} {c} {s} {al} {ct}\n", (k, f, c, s, al, ct))
        }
        .Paste(text) {
            try w.Str(if open { "Q " } else { "P " })
            for x in text.AsBytes() {
                try w.Bytes(ZzHex[int(x >> 4)..int(x >> 4) + 1])
                try w.Bytes(ZzHex[int(x & 15)..int(x & 15) + 1])
            }
            try w.Str("\n")
        }
        .Mouse(button, row, col, mods) {
            s := if mods.shift { 1 } else { 0 }
            al := if mods.alt { 1 } else { 0 }
            ct := if mods.ctrl { 1 } else { 0 }
            try Print(w, "M {button} {row} {col} {s} {al} {ct}\n", (button, row, col, s, al, ct))
        }
        .Focus(on) {
            o := if on { 1 } else { 0 }
            try Print(w, "F {o}\n", (o))
        }
        .Resize(cols, rows) {
            try Print(w, "R {cols} {rows}\n", (cols, rows))
        }
    }
}

const ZzHex: [16]u8 = [b'0', b'1', b'2', b'3', b'4', b'5', b'6', b'7', b'8', b'9', b'a', b'b', b'c', b'd', b'e', b'f']
`

// termReadMain serves framed scripts ("<len>\n" then len bytes), each in a
// fresh sub-arena.
const termReadMain = `import std.term

export Main(sys: &System): WriteError|ReadError|ParseError|Utf8Error|OutOfMemory!void {
    var inp := sys.Stdin()
    var out := sys.Stdout()
    var head: [32]u8 = [0; 32]
    data := sys.arena.Slice[u8](1 << 17).Must()
    for {
        h := try inp.Until(0x0A, mut head[..])
        if h == 0 {
            return
        }
        n := Cast[int](try (try str.From(head[0..h - 1])).ParseInt())
        got := try inp.ReadAll(mut data[0..n])
        var sub := try sys.arena.Sub()
        defer sub.Free()
        try term.ZzRead(mut out, &sub, data[0..got])
        try out.Flush()
    }
}
`

// termReadSrv is the running driver of termReadHook.
var termReadSrv struct {
	sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	err *bytes.Buffer
}

// termReadAsk returns the driver's lines for the script b.
func termReadAsk(t *testing.T, b []byte) []string {
	termReadSrv.Lock()
	defer termReadSrv.Unlock()
	if termReadSrv.cmd == nil {
		dir, err := packageTempDir("mktermread")
		if err != nil {
			t.Fatal(err)
		}
		stdDir := termStd(t, dir, map[string]string{"term/zz_read.mk": termReadHook})
		if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(termReadMain), 0o644); err != nil {
			t.Fatal(err)
		}
		exe := termExe(dir, "termread")
		t.Setenv("MK_CACHE", filepath.Join(dir, "cache"))
		out, status := mkCmd(t, dir, stdDir, termBuildArgs(exe)...)
		if status != 0 {
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			t.Fatalf("mk build: exit %d\n%s", status, out)
		}
		cmd := termDriverCmd(t, exe)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var eb bytes.Buffer
		cmd.Stderr = &eb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		termReadSrv.cmd, termReadSrv.in, termReadSrv.out, termReadSrv.err = cmd, stdin, bufio.NewReader(stdout), &eb
	}
	if _, err := fmt.Fprintf(termReadSrv.in, "%d\n%s", len(b), b); err != nil {
		t.Fatalf("driver gone: %v\n%s", err, termReadSrv.err.String())
	}
	var lines []string
	for {
		line, err := termReadSrv.out.ReadString('\n')
		if err != nil {
			termReadSrv.cmd = nil
			t.Fatalf("driver gone on script %x: %v\n%s", b, err, termReadSrv.err.String())
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "END" {
			return lines
		}
		lines = append(lines, line)
	}
}

// termReadStop ends the driver.
func termReadStop() {
	termReadSrv.Lock()
	defer termReadSrv.Unlock()
	if termReadSrv.cmd == nil {
		return
	}
	termReadSrv.in.Close()
	_ = termReadSrv.cmd.Wait()
	termReadSrv.cmd = nil
}

// termReadStream is the byte stream a script puts in the buffer: the 'x'
// bytes held at the start, then the UTF-8 of the units through Go's
// UTF-16 decoder (a unit 0 is no character; a high surrogate left at the
// end is still waiting, so it adds nothing).
func termReadStream(b []byte) (stream []byte, pasting bool) {
	start := int(binary.LittleEndian.Uint16(b[1:3])) % (4096 - 6 + 1)
	stream = bytes.Repeat([]byte("x"), start)
	var units []uint16
	if b[0]&2 != 0 {
		units = append(units, 0xd800)
	}
	for i := 3; i+1 < len(b); i += 2 {
		if u := binary.LittleEndian.Uint16(b[i:]); u != 0 {
			units = append(units, u)
		}
	}
	if n := len(units); n > 0 && units[n-1] >= 0xd800 && units[n-1] <= 0xdbff {
		units = units[:n-1]
	}
	for _, r := range utf16.Decode(units) {
		stream = utf8.AppendRune(stream, r)
	}
	return stream, b[0]&1 != 0
}

// termReadModel is what Read's state machine makes of the whole stream at
// once: Decode without final until it needs input, then with final until
// nothing is held, as ZzRead's end does. Its pastes have no buffer to cut
// them: termReadJoin joins the driver's pieces before the comparison.
func termReadModel(stream []byte, pasting bool) []string {
	var out []string
	end := []byte("\x1b[201~")
	i, skip := 0, 0
	call := func(final bool) bool {
		fin := final
		for {
			if pasting {
				k := bytes.Index(stream[i:], end)
				if k < 0 {
					return false
				}
				pasting = false
				if k > 0 {
					out = append(out, "P "+hex.EncodeToString([]byte(strings.ToValidUTF8(string(stream[i:i+k]), "\ufffd"))))
				}
				i += k + len(end)
				if k > 0 {
					return true
				}
				continue
			}
			if i >= len(stream) {
				return false
			}
			s := termStepOf(stream[i:], fin, skip)
			was := skip
			skip = termSkipOf(s.kind)
			if s.n == 0 && skip == was {
				return false
			}
			if s.n > 0 {
				fin = false
			}
			i += s.n
			switch s.kind {
			case 1:
				b := func(v bool) int {
					if v {
						return 1
					}
					return 0
				}
				out = append(out, fmt.Sprintf("K %d %d %d %d %d %d", s.key, s.f, s.ch, b(s.shift), b(s.alt), b(s.ctl)))
				return true
			case 2:
				pasting = true
			case 3:
				b := func(v bool) int {
					if v {
						return 1
					}
					return 0
				}
				out = append(out, fmt.Sprintf("M %d %d %d %d %d %d", s.a, s.b, s.c, b(s.shift), b(s.alt), b(s.ctl)))
				return true
			case 4:
				out = append(out, fmt.Sprintf("F %d", s.a))
				return true
			}
		}
	}
	for call(false) {
	}
	for {
		if call(true) {
			continue
		}
		if !(i < len(stream) && !pasting) {
			break
		}
	}
	p := 0
	if pasting {
		p = 1
	}
	return append(out, fmt.Sprintf("T %d %d", p, skip))
}

// termReadJoin joins each run of pieces ("Q") with the line after it, as
// one paste cut by a full buffer is one paste: the run ends as "P" when
// the paste ended, and stays "Q" when it is still open.
func termReadJoin(lines []string) []string {
	var out []string
	for _, l := range lines {
		if (strings.HasPrefix(l, "P ") || strings.HasPrefix(l, "Q ")) && len(out) > 0 && strings.HasPrefix(out[len(out)-1], "Q ") {
			out[len(out)-1] = l[:2] + out[len(out)-1][2:] + l[2:]
			continue
		}
		out = append(out, l)
	}
	return out
}

// termReadCheck runs the script b through the driver and compares it with
// the model. A paste still open at the end may have given out a prefix of
// its text (the pieces a full buffer cut); the model gives none, so the
// driver's last paste line, when the paste is open, must be a prefix of
// what the stream holds.
func termReadCheck(t *testing.T, b []byte) {
	t.Helper()
	if len(b) < 3 {
		return
	}
	got := termReadJoin(termReadAsk(t, b))
	stream, pasting := termReadStream(b)
	want := termReadJoin(termReadModel(stream, pasting))
	if len(got) > 0 && got[len(got)-1] == "STUCK" || len(got) > 1 && got[len(got)-2] == "STUCK" {
		t.Fatalf("script %x: stuck (Fill's guard or no progress at the end)\n got %q", b, got)
	}
	if len(got) > 1 && strings.HasPrefix(got[len(got)-2], "Q ") {
		// The open paste's pieces, which the model (no buffer) never gives
		// out: drop them, after checking the stream holds them.
		piece, _ := hex.DecodeString(got[len(got)-2][2:])
		if !bytes.Contains(stream, piece) {
			t.Fatalf("script %x: the open paste gave out bytes the stream does not hold: %q", b, piece)
		}
		got = append(got[:len(got)-2], got[len(got)-1])
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("script %x:\n got %q\nwant %q", b, got, want)
	}
}

// termReadScript is a script: flags, a starting tail and the units.
func termReadScript(flags byte, tail int, units ...uint16) []byte {
	b := []byte{flags, byte(tail), byte(tail >> 8)}
	for _, u := range units {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

// termUnits are the UTF-16 units of s.
func termUnits(s string) []uint16 { return utf16.Encode([]rune(s)) }

// termReadRecorded are the scripts of the edges: a lone high surrogate before
// a unit that is no low surrogate at 4 to 9 bytes free, pastes that fill
// the buffer, markers cut by the batches, long sequences across batches.
func termReadRecorded() [][]byte {
	var out [][]byte
	for free := 6; free <= 12; free++ {
		out = append(out, termReadScript(3, 4096-free, 0x20ac, 0xd800, 0x20ac, 0xd800, 0x20ac))
		out = append(out, termReadScript(2, 4096-free, 0x20ac, 0xd800, 0xd800, 0xdc00))
	}
	big := termUnits("\x1b[200~" + strings.Repeat("p\u00e9\u20ac\U0001F600", 1500) + "\x1b[201~q")
	out = append(out, termReadScript(0, 0, big...))
	out = append(out, termReadScript(1, 4000, termUnits(strings.Repeat("\u20ac", 300)+"\x1b[201~a")...))
	out = append(out, termReadScript(0, 0, termUnits("a\x1b["+strings.Repeat("1", 500)+"Ab")...))
	out = append(out, termReadScript(0, 0, termUnits("a\x1b]52;c;"+strings.Repeat("QUFB", 800)+"\x1b\\b")...))
	out = append(out, termReadScript(0, 0, termUnits("\x1b")...))
	out = append(out, termReadScript(0, 0, termUnits("\x1b]2;t\x1b")...))
	out = append(out, termReadScript(1, 0, termUnits(strings.Repeat("z\x1b", 3000))...))
	return out
}

// TestTerm_ReadRecorded compares Read's state machine with the model over
// the recorded scripts.
func TestTerm_ReadRecorded(t *testing.T) {
	t.Cleanup(termReadStop)
	for _, b := range termReadRecorded() {
		termReadCheck(t, b)
	}
}

// FuzzTerm_Read is Read's fuzz target: a panic (a write past the buffer, a
// bounds check), a hang, Fill's guard, or an event stream other than the
// model's over the whole stream fails.
func FuzzTerm_Read(f *testing.F) {
	for _, b := range termReadRecorded() {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<16 {
			b = b[:1<<16]
		}
		termReadCheck(t, b)
	})
}

// termLinuxFuzz is the Linux leg of std.term's fuzz targets:
// with MK_TERM_FUZZ_LINUX=1 the drivers of FuzzTerm_Parse,
// FuzzTerm_Sanitize and FuzzTerm_Read are built for x86_64-linux and each
// runs in WSL behind one wsl.exe client for the whole run of its fuzz
// worker. It needs MK_LINUX=1 and the WSL lock (go test -p 1, -parallel 1:
// one client at a time).
func termLinuxFuzz() bool { return os.Getenv("MK_TERM_FUZZ_LINUX") == "1" }

// termExe is a driver's path in dir: an .exe on Windows unless the driver
// is built for Linux.
func termExe(dir, name string) string {
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" && !termLinuxFuzz() {
		exe += ".exe"
	}
	return exe
}

// termBuildArgs are mk's arguments to build a driver's main.mk into exe.
func termBuildArgs(exe string) []string {
	args := []string{"build", "-o", exe}
	if termLinuxFuzz() {
		args = append(args, "--target", "x86_64-linux")
	}
	return append(args, "main.mk")
}

// termDriverCmd starts the driver exe: in WSL for the Linux leg, under its
// guards.
func termDriverCmd(t *testing.T, exe string) *exec.Cmd {
	if termLinuxFuzz() {
		linuxGate(t)
		wslGuard()
		return exec.Command("wsl.exe", "-e", wslPath(exe))
	}
	return exec.Command(exe)
}

// termShortHook is the extra std/term file of TestTerm_ShortWrites: Close's
// first half (Leave) and ReadSecret's line end over std-io's fault table
// (core/io_zfault.mk), whose writes are short (mode 6: 2 bytes a call) or
// report 0 (mode 7).
const termShortHook = `import std.core

ZzShort(mouse: bool, alt: bool): Term {
    return Term(input: 0, out: 0, is_raw: false, saved_len: 0, saved: [0; 64], alt: alt, alt_at: 0,
        in_buf: [0; 4096], head: 0, tail: 0, holding: false, held_at: 0, pasting: false, high: 0, resized: false, empties: 0, cols: 0,
        rows: 0, reg: -1, mouse: mouse, mouse_at: 0, skip: 0, scan: 0, secret: [0; 1024], secret_len: 0,
        secret_raw: false, kitty: KittyZero())
}

ZzDrop(t: Term) {
    Term(input: _, out: _, is_raw: _, saved_len: _, saved: _, alt: _, alt_at: _, in_buf: _, head: _, tail: _, holding: _, held_at: _, pasting: _,
        high: _, resized: _, empties: _, cols: _, rows: _, reg: _, mouse: _, mouse_at: _, skip: _, scan: _, secret: _, secret_len: _,
        secret_raw: _, kitty: _) := t
}

test "TERM-8: Close writes all of MouseOff and AltLeave when every write is short" (sys: &System) {
    io := core.FaultIo(6, 0, 0, 0)
    var t := ZzShort(true, true)
    t.Leave(io)
    // 32 bytes of MouseOff and 8 of AltLeave, 2 a write.
    Assert(core.FaultCalls() == 20 && !t.mouse && !t.alt)
    ZzDrop(t)
}

test "TERM-8: a write that reports 0 bytes ends the loop" (sys: &System) {
    io := core.FaultIo(7, 0, 0, 0)
    var t := ZzShort(true, true)
    t.Leave(io)
    Assert(core.FaultCalls() == 2)
    ZzDrop(t)
}

test "TERM-8: ReadSecret's line end is written whole" (sys: &System) {
    io := core.FaultIo(6, 0, 0, 0)
    var t := ZzShort(false, false)
    t.EndSecret(io, true, true)
    Assert(core.FaultCalls() == 1)
    var u := ZzShort(false, false)
    _ = WriteAll(io, 0, "\r\n\r\n\r".AsBytes())
    Assert(core.FaultCalls() == 4)
    ZzDrop(t)
    ZzDrop(u)
}

test "TERM-8, R2-03: a Writer's send whose writes fail records the alternate screen and mouse capture it may have set" (sys: &System) {
    // Every write fails: none of the bytes is known to be out, and both
    // sets are taken as done.
    io := core.FaultIo(3, 0, 0, 0)
    var t := ZzShort(false, false)
    var failed := false
    TermSend(mut t, io, "\x1b[?1049h\x1b[?1000hx".AsBytes()) else {
        failed = true
    }
    Assert(failed && t.alt && t.mouse && core.FaultCalls() == 1)
    // Short writes that all go through: the state after every byte, a
    // leave included.
    io2 := core.FaultIo(6, 0, 0, 0)
    var u := ZzShort(false, false)
    TermSend(mut u, io2, "\x1b[?1049h\x1b[?1049l".AsBytes()).Must()
    Assert(!u.alt && core.FaultCalls() == 8)
    // A count of 0 ends the loop after the first write: nothing out,
    // the set taken as done.
    io3 := core.FaultIo(7, 0, 0, 0)
    var v := ZzShort(false, false)
    TermSend(mut v, io3, "\x1b[?1049h".AsBytes()) else {}
    Assert(v.alt)
    ZzDrop(t)
    ZzDrop(u)
    ZzDrop(v)
}
`

// TestTerm_ShortWrites runs termShortHook's blocks over a copy of
// std with std-io's fault table.
func TestTerm_ShortWrites(t *testing.T) {
	root := t.TempDir()
	stdDir := termStd(t, root, map[string]string{"core/io_zfault.mk": faultCore, "term/zz_short.mk": termShortHook})
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	out, status := mkCmd(t, root, stdDir, "test", "--std=std.term", "-run", "TERM-8")
	m := termSummary.FindStringSubmatch(out)
	if status != 0 || m == nil || m[2] != "0" || m[1] != "4" {
		t.Fatalf("mk test -run TERM-8: exit %d\n%s", status, out)
	}
}
