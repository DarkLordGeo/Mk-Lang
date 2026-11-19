// Why: std.term's input decoder turns untrusted terminal bytes into keys,
// and Go has no terminal decoder to compare it with.
// What: termModel, a second decoder in Go, and TestTerm_DecodeRecorded,
// DecodeGenerated, DecodeLongSequences and FuzzTerm_Parse.
// How: a driver built over a copy of std with termFuzzHook added decodes
// each stream; every step must equal termModel's.

// The oracle and the
// fuzz target of std.term's input decoder (std/term/input.mk).
// Go's standard library has no terminal decoder, so the oracle is
// termModel below: a second implementation of the same mapping, written
// from xterm's ctlseqs ("PC-Style Function Keys") and Unicode 3.9's
// maximal-subpart rule, compared step by step with the Mk decoder over
// recorded sequences and generated streams. The Mk side is a driver built
// over a copy of std with one extra file in std/term (termFuzzHook), since
// the decoder is internal to the module.
//
//	go test ./std -run TestTerm_Decode
//	go test ./std -run '^$' -fuzz FuzzTerm_Parse -fuzztime 5m -parallel 2
//
// MK_TERM_BASE, when set, is a std directory to copy instead of the tree's;
// std/term always comes from the tree.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// termFuzzHook is the extra std/term file: ZzDecode writes every step of
// Step over b with final true, the skip state carried from step to step,
// one line each ("n kind key f ch shift alt ctrl a b c"), then the first
// step with final false as "first n kind key f ch shift alt ctrl a b c",
// then "END". A step of no bytes that leaves the state as it was is STUCK.
const termFuzzHook = `export ZzDecode(w: mut Writer, b: []u8): WriteError!void {
    var i := 0
    var skip: u8 = 0
    for i < b.Len() {
        p := Step(b[i..], true, skip)
        try ZzStep(w, "", &p)
        was := skip
        skip = SkipOf(p.kind)
        if p.n <= 0 && skip == was {
            try w.Str("STUCK\n")
            return
        }
        i += p.n
    }
    if b.Len() > 0 {
        p := Step(b, false, 0)
        try ZzStep(w, "first ", &p)
    }
    try w.Str("END\n")
}

ZzStep(w: mut Writer, pre: str, p: &Parsed): WriteError!void {
    n := p.n
    kind := p.kind
    var f := 0
    key := match p.key {
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
        .F(n: k) {
            f = k
            15
        }
    }
    ch := u32(p.ch)
    s := if p.mods.shift { 1 } else { 0 }
    a := if p.mods.alt { 1 } else { 0 }
    c := if p.mods.ctrl { 1 } else { 0 }
    pa := p.a
    pb := p.b
    pc := p.c
    try Print(w, "{pre}{n} {kind} {key} {f} {ch} {s} {a} {c} {pa} {pb} {pc}\n", (pre, n, kind, key, f, ch, s, a, c, pa, pb, pc))
}
`

// termFuzzMain serves framed inputs ("<len>\n" then len bytes) until the
// end of its input.
const termFuzzMain = `import std.term

export Main(sys: &System): WriteError|ReadError|ParseError|Utf8Error!void {
    var inp := sys.Stdin()
    var out := sys.Stdout()
    var head: [32]u8 = [0; 32]
    data := sys.arena.Slice[u8](1 << 16).Must()
    for {
        h := try inp.Until(0x0A, mut head[..])
        if h == 0 {
            return
        }
        n := Cast[int](try (try str.From(head[0..h - 1])).ParseInt())
        got := try inp.ReadAll(mut data[0..n])
        try term.ZzDecode(mut out, data[0..got])
        try out.Flush()
    }
}
`

// termStd copies the std to build against (MK_TERM_BASE or the tree's)
// into root, with the tree's std/term and the extra files.
func termStd(t testing.TB, root string, extra map[string]string) string {
	t.Helper()
	base := os.Getenv("MK_TERM_BASE")
	if base == "" {
		base = "."
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(stdDir, "term")); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(stdDir, "term"), os.DirFS("term")); err != nil {
		t.Fatal(err)
	}
	for p, text := range extra {
		if err := os.WriteFile(filepath.Join(stdDir, filepath.FromSlash(p)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stdDir
}

// termBuildDriver builds the decode driver into dir and returns its path.
func termBuildDriver(t *testing.T, dir string) string {
	t.Helper()
	stdDir := termStd(t, dir, map[string]string{"term/zz_fuzz.mk": termFuzzHook})
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(termFuzzMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := termExe(dir, "termdec")
	t.Setenv("MK_CACHE", filepath.Join(dir, "cache"))
	out, status := mkCmd(t, dir, stdDir, termBuildArgs(exe)...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// termSrv is the running driver, shared by the inputs of one process.
var termSrv struct {
	sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	err *bytes.Buffer
}

// termAsk returns the driver's lines for b, starting it first.
func termAsk(t *testing.T, b []byte) []string {
	termSrv.Lock()
	defer termSrv.Unlock()
	if termSrv.cmd == nil {
		dir, err := packageTempDir("mkterm")
		if err != nil {
			t.Fatal(err)
		}
		exe := termBuildDriver(t, dir)
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
		termSrv.cmd, termSrv.in, termSrv.out, termSrv.err = cmd, stdin, bufio.NewReader(stdout), &eb
	}
	if _, err := fmt.Fprintf(termSrv.in, "%d\n%s", len(b), b); err != nil {
		t.Fatalf("driver gone: %v\n%s", err, termSrv.err.String())
	}
	var lines []string
	for {
		line, err := termSrv.out.ReadString('\n')
		if err != nil {
			termSrv.cmd = nil
			t.Fatalf("driver gone on input %q: %v\n%s", b, err, termSrv.err.String())
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "END" {
			return lines
		}
		lines = append(lines, line)
	}
}

// termStop ends the driver.
func termStop() {
	termSrv.Lock()
	defer termSrv.Unlock()
	if termSrv.cmd == nil {
		return
	}
	termSrv.in.Close()
	_ = termSrv.cmd.Wait()
	termSrv.cmd = nil
}

// The model's step: as Parsed, with Key as its number (Char 0 .. Delete
// 14, F 15 with f) and the kinds of input.mk (0 dropped, 1 key, 2 paste,
// 3 mouse and 4 focus, with Parsed's a, b and c; 5 and 6 drop the start of
// a CSI or a control string that goes on, skipped by termSkip).
type termStep struct {
	n, kind, key, f int
	ch              rune
	shift, alt, ctl bool
	a, b, c         int
}

func (s termStep) String() string {
	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	return fmt.Sprintf("%d %d %d %d %d %d %d %d %d %d %d", s.n, s.kind, s.key, s.f, s.ch, b(s.shift), b(s.alt), b(s.ctl), s.a, s.b, s.c)
}

const (
	tkChar = iota
	tkEnter
	tkEscape
	tkBackspace
	tkTab
	tkUp
	tkDown
	tkLeft
	tkRight
	tkHome
	tkEnd
	tkPageUp
	tkPageDown
	tkInsert
	tkDelete
	tkF
)

func tKey(n, key int, ch rune) termStep { return termStep{n: n, kind: 1, key: key, ch: ch} }
func tDrop(n int) termStep              { return termStep{n: n} }
func tAlt(s termStep, n int) termStep   { s.n = n; s.alt = true; return s }

// termPointer, when set (std-term_mouse_test.go, the cuttable mouse and
// focus unit), decodes the reports of a whole CSI, or says false for
// termCsiFinal.
var termPointer func(b []byte, n int, v [8]int, count int, private, f byte, final bool) (termStep, bool)

// termKitty, when set (std-term_kitty_test.go, the cuttable kitty unit),
// decodes the keys of a whole CSI (second is the second field's
// sub-parameter, -1 none), or says false for termCsiFinal.
var termKitty func(n int, v [8]int, count, second int, private, f byte) (termStep, bool)

// termStepOf is the oracle's Step: termSkip while a sequence is skipped
// (skip 5 or 6), else termModel.
func termStepOf(b []byte, final bool, skip int) termStep {
	if skip != 0 {
		return termSkip(b, skip, final)
	}
	return termModel(b, final)
}

// termSkipOf is the decoder's state after a step of kind k.
func termSkipOf(k int) int {
	if k == 5 || k == 6 {
		return k
	}
	return 0
}

// termSkip drops what is left of a sequence: DEC's VT500 csi_ignore (to a
// final byte 0x40-0x7E, consumed, or before any byte outside 0x20-0x7E)
// and its string states (to BEL, CAN or SUB, consumed; to ST, consumed; or
// before an escape that starts something else).
func termSkip(b []byte, state int, final bool) termStep {
	for i, c := range b {
		if state == 5 {
			switch {
			case c >= 0x40 && c <= 0x7e:
				return tDrop(i + 1)
			case c < 0x20 || c > 0x7e:
				return tDrop(i)
			}
			continue
		}
		switch c {
		case 0x07, 0x18, 0x1a:
			return tDrop(i + 1)
		case 0x1b:
			if i+1 < len(b) {
				if b[i+1] == '\\' {
					return tDrop(i + 2)
				}
				return tDrop(i)
			}
			if final {
				return tDrop(i)
			}
			return termStep{n: i, kind: state}
		}
	}
	return termStep{n: len(b), kind: state}
}

// termString is the oracle for ESC and a control string's opener (] P _ ^
// X): dropped through its end (BEL, ST, or CAN and SUB, which abort it);
// skipped (kind 6) past 64 bytes with no end; the opener as a key with alt
// when an escape that starts something else comes first (no terminal
// sends a string so cut) or, final, when no end came; else more
// bytes.
func termString(b []byte, final bool) termStep {
	for i := 2; ; i++ {
		if i >= 64 {
			return termStep{n: i, kind: 6}
		}
		if i >= len(b) {
			if !final {
				return tDrop(0)
			}
			return termAltKey(b)
		}
		switch b[i] {
		case 0x07, 0x18, 0x1a:
			return tDrop(i + 1)
		case 0x1b:
			if i+1 >= len(b) {
				if final {
					return termAltKey(b)
				}
				return tDrop(0)
			}
			if b[i+1] == '\\' {
				return tDrop(i + 2)
			}
			return termAltKey(b)
		}
	}
}

// termAltKey is ESC and the ASCII opener b[1] as one key with alt.
func termAltKey(b []byte) termStep {
	return tAlt(termSingle(b[1:], true), 2)
}

func termIsString(c byte) bool {
	return c == ']' || c == 'P' || c == '_' || c == '^' || c == 'X'
}

// termModel is the oracle's Parse.
func termModel(b []byte, final bool) termStep {
	if b[0] == 0x1b {
		return termEscaped(b, final)
	}
	return termSingle(b, final)
}

func termSingle(b []byte, final bool) termStep {
	c := b[0]
	if c >= 0x80 {
		u, n := termUtf8(b, final)
		if n == 0 {
			return tDrop(0)
		}
		return tKey(n, tkChar, u)
	}
	if c >= 0x20 && c != 0x7f {
		return tKey(1, tkChar, rune(c))
	}
	s := tKey(1, tkChar, 0)
	switch {
	case c == 0x0d:
		s.key = tkEnter
	case c == 0x09:
		s.key = tkTab
	case c == 0x7f:
		s.key = tkBackspace
	case c == 0:
		s.ch, s.ctl = ' ', true
	case c <= 0x1a:
		s.ch, s.ctl = rune('a'+c-1), true
	default:
		s.ch, s.ctl = rune('4'+c-0x1c), true
	}
	return s
}

func termEscaped(b []byte, final bool) termStep {
	esc := tKey(1, tkEscape, 0)
	if len(b) == 1 {
		if final {
			return esc
		}
		return tDrop(0)
	}
	switch b[1] {
	case '[':
		return termCsi(b, final)
	case 'O':
		return termSs3(b, final)
	case ']', 'P', '_', '^', 'X':
		return termString(b, final)
	case 0x1b:
		altEsc := tAlt(tKey(2, tkEscape, 0), 2)
		if len(b) == 2 {
			if final {
				return altEsc
			}
			return tDrop(0)
		}
		if b[2] == '[' || b[2] == 'O' || termIsString(b[2]) {
			var p termStep
			switch {
			case b[2] == '[':
				p = termCsi(b[1:], final)
			case b[2] == 'O':
				p = termSs3(b[1:], final)
			default:
				p = termString(b[1:], final)
			}
			if p.n == 0 {
				return p
			}
			if p.n == 1 {
				return altEsc
			}
			if p.kind != 1 {
				return esc
			}
			return tAlt(p, p.n+1)
		}
		return altEsc
	}
	p := termSingle(b[1:], final)
	if p.n == 0 {
		if final {
			return esc
		}
		return p
	}
	return tAlt(p, p.n+1)
}

func termSs3(b []byte, final bool) termStep {
	if len(b) < 3 {
		if final {
			// ESC O alone is Alt+Shift+O.
			return termAltKey(b)
		}
		return tDrop(0)
	}
	m := map[byte]int{'A': tkUp, 'B': tkDown, 'C': tkRight, 'D': tkLeft, 'H': tkHome, 'F': tkEnd, 'M': tkEnter}
	if k, ok := m[b[2]]; ok {
		return tKey(3, k, 0)
	}
	if b[2] >= 'P' && b[2] <= 'S' {
		s := tKey(3, tkF, 0)
		s.f = int(b[2]-'P') + 1
		return s
	}
	return tDrop(3)
}

// termParamMax is where a CSI parameter saturates: one past U+10FFFF.
const termParamMax = 0x110000

func termCsi(b []byte, final bool) termStep {
	const max = 64
	var v [8]int
	private := byte(0)
	i := 2
	if i < len(b) && b[i] >= 0x3c && b[i] <= 0x3f {
		private = b[i]
		i++
	}
	field, sub, digits, second := 0, 0, false, -1
	for {
		if i >= max {
			return termStep{n: i, kind: 5}
		}
		if i >= len(b) {
			if final {
				if len(b) == 2 {
					// ESC [ alone is Alt+[.
					return termAltKey(b)
				}
				return tKey(1, tkEscape, 0)
			}
			return tDrop(0)
		}
		c := b[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
			if field < 8 && sub == 0 {
				v[field] = min(v[field]*10+int(c-'0'), termParamMax)
			} else if field == 1 && sub == 1 {
				second = min(termNonNeg(second)*10+int(c-'0'), termParamMax)
			}
			i++
			continue
		case c == ';':
			field, sub = field+1, 0
			i++
			continue
		case c == ':':
			sub++
			i++
			continue
		case c >= 0x20 && c <= 0x2f, c >= 0x3c && c <= 0x3f:
			// An intermediate, or a private marker out of place:
			// csi_ignore, the sequence dropped to its final byte.
			for k := i; ; k++ {
				if k >= max {
					return termStep{n: k, kind: 5}
				}
				if k >= len(b) {
					if final {
						return tKey(1, tkEscape, 0)
					}
					return tDrop(0)
				}
				if b[k] >= 0x40 && b[k] <= 0x7e {
					return tDrop(k + 1)
				}
				if b[k] < 0x20 || b[k] > 0x7e {
					return tDrop(k)
				}
			}
		case c >= 0x40 && c <= 0x7e:
			count := 0
			if digits || field > 0 {
				count = field + 1
			}
			if termPointer != nil {
				if s, ok := termPointer(b, i+1, v, count, private, c, final); ok {
					return s
				}
			}
			if termKitty != nil {
				if s, ok := termKitty(i+1, v, count, second, private, c); ok {
					return s
				}
			}
			return termCsiFinal(i+1, v, count, private, c)
		default:
			return tDrop(i)
		}
	}
}

func termCsiFinal(n int, v [8]int, count int, private, f byte) termStep {
	if private != 0 {
		return tDrop(n)
	}
	var s termStep
	switch f {
	case 'A', 'B', 'C', 'D', 'H', 'F':
		s = tKey(n, map[byte]int{'A': tkUp, 'B': tkDown, 'C': tkRight, 'D': tkLeft, 'H': tkHome, 'F': tkEnd}[f], 0)
	case 'P', 'Q', 'R', 'S':
		if f == 'R' && count == 2 && v[0] != 1 {
			return tDrop(n)
		}
		s = tKey(n, tkF, 0)
		s.f = int(f-'P') + 1
	case 'Z':
		s = tKey(n, tkTab, 0)
		s.shift = true
		return s
	case '~':
		if v[0] == 200 {
			return termStep{n: n, kind: 2}
		}
		k, f, ok := termTilde(v[0])
		if !ok {
			return tDrop(n)
		}
		s = tKey(n, k, 0)
		s.f = f
	default:
		return tDrop(n)
	}
	if count >= 2 && v[1] > 1 {
		m := v[1] - 1
		s.shift, s.alt, s.ctl = m&1 != 0, m&2 != 0, m&4 != 0
	}
	return s
}

func termTilde(v int) (int, int, bool) {
	switch {
	case v == 1 || v == 7:
		return tkHome, 0, true
	case v == 2:
		return tkInsert, 0, true
	case v == 3:
		return tkDelete, 0, true
	case v == 4 || v == 8:
		return tkEnd, 0, true
	case v == 5:
		return tkPageUp, 0, true
	case v == 6:
		return tkPageDown, 0, true
	case v >= 11 && v <= 15:
		return tkF, v - 10, true
	case v >= 17 && v <= 21:
		return tkF, v - 11, true
	case v >= 23 && v <= 26:
		return tkF, v - 12, true
	case v == 28 || v == 29:
		return tkF, v - 13, true
	case v >= 31 && v <= 34:
		return tkF, v - 14, true
	}
	return 0, 0, false
}

// termUtf8 is the scalar at b and its length; U+FFFD and the maximal
// subpart's length when ill formed; 0, 0 when b ends inside a sequence
// that may still be well formed and final is false. Go's own decoder is
// the check of the well-formed case.
func termUtf8(b []byte, final bool) (rune, int) {
	c := b[0]
	need, lo, hi := 0, byte(0x80), byte(0xbf)
	switch {
	case c >= 0xc2 && c <= 0xdf:
		need = 1
	case c >= 0xe0 && c <= 0xef:
		need = 2
		if c == 0xe0 {
			lo = 0xa0
		} else if c == 0xed {
			hi = 0x9f
		}
	case c >= 0xf0 && c <= 0xf4:
		need = 3
		if c == 0xf0 {
			lo = 0x90
		} else if c == 0xf4 {
			hi = 0x8f
		}
	default:
		return utf8.RuneError, 1
	}
	for k := 1; k <= need; k++ {
		if k >= len(b) {
			if final {
				return utf8.RuneError, k
			}
			return 0, 0
		}
		if b[k] < lo || b[k] > hi {
			return utf8.RuneError, k
		}
		lo, hi = 0x80, 0xbf
	}
	r, n := utf8.DecodeRune(b)
	if n != need+1 {
		panic(fmt.Sprintf("model and utf8 disagree on % x", b[:need+1]))
	}
	return r, n
}

// termExpect is what the driver must print for b.
func termExpect(b []byte) []string {
	var out []string
	skip := 0
	for i := 0; i < len(b); {
		s := termStepOf(b[i:], true, skip)
		out = append(out, s.String())
		was := skip
		skip = termSkipOf(s.kind)
		if s.n <= 0 && skip == was {
			return append(out, "STUCK")
		}
		i += s.n
	}
	if len(b) > 0 {
		out = append(out, "first "+termStepOf(b, false, 0).String())
	}
	return out
}

// termRecorded are the byte streams terminals send, each as xterm's
// ctlseqs, VTE and the Windows console (with virtual-terminal input) write
// them, with the edge cases of the decoder.
var termRecorded = []string{
	"a", "Z", "~", " ", "\r", "\n", "\t", "\x7f", "\x08", "\x00", "\x01", "\x1a", "\x1c", "\x1f",
	"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D", "\x1b[H", "\x1b[F", "\x1bOA", "\x1bOD", "\x1bOH", "\x1bOF",
	"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", "\x1bOM", "\x1b[1;2A", "\x1b[1;3B", "\x1b[1;5C", "\x1b[1;8D",
	"\x1b[1;5P", "\x1b[1;2R", "\x1b[12;40R", "\x1b[2~", "\x1b[3~", "\x1b[5~", "\x1b[6~", "\x1b[1~", "\x1b[4~",
	"\x1b[7~", "\x1b[8~", "\x1b[15~", "\x1b[17~", "\x1b[24~", "\x1b[34~", "\x1b[3;5~", "\x1b[5;3~", "\x1b[Z",
	"\x1b[200~", "\x1b[201~", "\x1b[I", "\x1b[O", "\x1b[<0;10;5M", "\x1b[<0;10;5m", "\x1b[M !!",
	"\x1b[97;5u", "\x1b[?1;2c", "\x1b[ q", "\x1b[1;2;3;4;5;6;7;8;9;10A", "\x1b[99999999999A",
	"\x1bx", "\x1b\r", "\x1b\x7f", "\x1b\x1b", "\x1b\x1b[A", "\x1b\x1bOP", "\x1b\x1bx", "\x1b", "\x1b[",
	"\x1b[1;", "\x1bO", "\x1b[\x01A", "\x1b[1\x1b[A", "é", "€", "😀", "\xc3", "\xe2\x82", "\xf0\x9f\x98",
	"\xc0\x80", "\xe0\x80\x80", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xf5", "\xff", "\x80", "\x1b\xc3\xa9",
	strings.Repeat("\x1b[", 40), "\x1b[" + strings.Repeat("1", 70) + "A", "\x1b[" + strings.Repeat(";", 70),
	// Long CSIs, control strings and their ends.
	"\x1b[" + strings.Repeat(" ", 70) + "qz", "\x1b[" + strings.Repeat("9", 200) + "\r", "\x1b]11;rgb:0000/0000/0000\x1b\\",
	"\x1b]10;rgb:ffff/ffff/ffff\x07", "\x1bP1$r0m\x1b\\", "\x1b_Gi=1;OK\x1b\\", "\x1b^pm\x1b\\", "\x1bXsos\x1b\\",
	"\x1b]2;t\x18z", "\x1b]2;t\x1b[A", "\x1b]52;c;" + strings.Repeat("QUFB", 300) + "\x07k", "\x1b]", "\x1bPab",
	"\x1b\x1b]11;x\x07", "\x1b]" + strings.Repeat("y", 100), "\x1b]2;t\x1b",
	// A string cut by another escape, a lone ESC O or ESC [, a private
	// marker out of place, and saturated parameters.
	"\x1bXab\x1b[A", "\x1b]\x1b]", "\x1bPab\x1b", "\x1b]2;t\x1b\\\x1b]2;t\x07", "\x1b[1<5u", "\x1b[2;?7~z",
	"\x1b[1;5>9;=A", "\x1b[1<\r", "\x1b\x1b[", "\x1b\x1bO", "\x1b[1048576u", "\x1b[1114111;5u", "\x1b[1114112u",
	"\x1bX" + strings.Repeat("k", 80) + "\x1b[A", "\x1b[<1;1114200;3M",
}

// termGen is a stream biased towards the decoder's edges.
func termGen(r *rand.Rand) []byte {
	alpha := []string{"\x1b", "[", "O", "0", "1", "2", "5", "9", ";", ":", "~", "A", "C", "H", "P", "R", "Z",
		"u", "M", "<", "?", " ", "a", "\r", "\x7f", "\x00", "\x03", "\x1c", "\xc3", "\xa9", "\xe2", "\x82",
		"\xf0", "\x9f", "\x80", "\xff", "200~", "201~", "1;5", "m", "I", "<0;", "!", "64;",
		"]", "_", "^", "X", "\x07", "\x18", "\\", strings.Repeat("7", 30), strings.Repeat(" ", 20)}
	n := r.Intn(24)
	var b []byte
	for range n {
		b = append(b, alpha[r.Intn(len(alpha))]...)
	}
	return b
}

func termCheck(t *testing.T, b []byte) {
	t.Helper()
	got := termAsk(t, b)
	want := termExpect(b)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("input %q:\n got %q\nwant %q", b, got, want)
	}
}

// termHasKeyInside reports a key step taken from inside a control
// sequence or string, which must never happen; checked without the
// model. b is a key 'a', a sequence, and a key 'b'; only those two may be
// keys.
func termHasKeyInside(lines []string) bool {
	keys := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "first ") || l == "END" {
			continue
		}
		if f := strings.Fields(l); len(f) > 1 && f[1] == "1" {
			keys++
		}
	}
	return keys != 2
}

// TestTerm_DecodeLongSequences checks that property without the model:
// a CSI, an intermediate CSI and each control string, of every body length
// from 0 to 300, between the keys a and b decode to those two keys only.
func TestTerm_DecodeLongSequences(t *testing.T) {
	t.Cleanup(termStop)
	for k := 0; k <= 300; k += 7 {
		for _, seq := range []string{
			"\x1b[" + strings.Repeat("9", k) + "~",
			"\x1b[" + strings.Repeat(" ", k) + "q",
			"\x1b]" + strings.Repeat("x", k) + "\x07",
			"\x1bP" + strings.Repeat("x", k) + "\x1b\\",
			"\x1b_" + strings.Repeat("x", k) + "\x1b\\",
			"\x1b^" + strings.Repeat("x", k) + "\x18",
			"\x1bX" + strings.Repeat("x", k) + "\x1a",
		} {
			b := []byte("a" + seq + "b")
			got := termAsk(t, b)
			if termHasKeyInside(got) {
				t.Fatalf("input %q: a key from inside the sequence:\n%q", b, got)
			}
		}
	}
}

// TestTerm_DecodeRecorded compares the decoder with the model over every
// recorded stream, alone and concatenated.
func TestTerm_DecodeRecorded(t *testing.T) {
	t.Cleanup(termStop)
	var all []byte
	for _, s := range termRecorded {
		termCheck(t, []byte(s))
		all = append(all, s...)
	}
	termCheck(t, all)
}

// TestTerm_DecodeGenerated compares the decoder with the model over 20,000
// generated streams (seed 8).
func TestTerm_DecodeGenerated(t *testing.T) {
	t.Cleanup(termStop)
	r := rand.New(rand.NewSource(8))
	for range 20_000 {
		termCheck(t, termGen(r))
	}
}

// FuzzTerm_Parse is the decoder's fuzz target: every input is decoded by the Mk
// decoder in a long-running driver (a dev build, every check on); a panic,
// a hang, a step of no bytes or a step that differs from the model fails.
func FuzzTerm_Parse(f *testing.F) {
	for _, s := range termRecorded {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 4096 {
			b = b[:4096]
		}
		termCheck(t, b)
	})
}

// termNonNeg is v, or 0 when v is negative.
func termNonNeg(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
