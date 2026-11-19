// Why: term.Sanitize is the entry that writes untrusted text to a terminal;
// a control byte it lets through can drive the terminal.
// What: termSanitizeModel, TestTerm_SanitizeOracle and FuzzTerm_Sanitize.
// How: the model works over runes decoded by unicode/utf8, the Mk function
// over bytes with a vector scan; their outputs must match.

// The oracle and the
// fuzz target of term.Sanitize (std/term/screen.mk), the
// entry that writes untrusted text. No standard library has a terminal
// sanitizer, so the oracle is termSanitizeModel below: a second
// implementation over runes, decoded with Go's unicode/utf8, where the Mk
// function works on bytes with a vector scan and a staging block. Each
// input goes to a driver (a dev build, every check on) that runs
// term.Sanitize into a Text and prints the result.
//
//	go test ./std -run TestTerm_Sanitize
//	go test ./std -run '^$' -fuzz FuzzTerm_Sanitize -fuzztime 5m -parallel 2
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// termSanMain serves framed inputs ("<len>\n" then len bytes): for each it
// prints "INVALID\n" when the bytes are not UTF-8, else the length of
// term.Sanitize's output, a newline, and the output.
const termSanMain = `import std.term

export Main(sys: &System): WriteError|ReadError|ParseError|Utf8Error|OutOfMemory!void {
    var inp := sys.Stdin()
    var out := sys.Stdout()
    var head: [32]u8 = [0; 32]
    data := try sys.arena.Slice[u8](1 << 16)
    for {
        h := try inp.Until(0x0A, mut head[..])
        if h == 0 {
            return
        }
        n := Cast[int](try (try str.From(head[0..h - 1])).ParseInt())
        got := try inp.ReadAll(mut data[0..n])
        s := str.From(data[0..got]) else {
            try out.Str("INVALID\n")
            try out.Flush()
            continue
        }
        var sub := try sys.arena.Sub()
        defer sub.Free()
        var t := Text.New(&sub)
        var w := t.Writer()
        try term.Sanitize(mut w, s)
        try w.Flush()
        o := t.Str()
        m := o.Len()
        try Print(mut out, "{m}\n", (m))
        try out.Str(o)
        try out.Flush()
    }
}
`

// termSanitizeModel is Sanitize over runes: every C0 control but line
// feed and tab, DEL and every C1 control become U+FFFD; s is valid UTF-8.
func termSanitizeModel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
			b.WriteRune(utf8.RuneError)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// termSan is the running driver, shared by the inputs of one process.
var termSan struct {
	sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	err *bytes.Buffer
}

// termSanAsk returns the driver's answer for b: its output, or "INVALID"
// with ok false.
func termSanAsk(t *testing.T, b []byte) (string, bool) {
	termSan.Lock()
	defer termSan.Unlock()
	if termSan.cmd == nil {
		dir, err := packageTempDir("mktermsan")
		if err != nil {
			t.Fatal(err)
		}
		stdDir := termStd(t, dir, nil)
		if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(termSanMain), 0o644); err != nil {
			t.Fatal(err)
		}
		exe := termExe(dir, "termsan")
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
		termSan.cmd, termSan.in, termSan.out, termSan.err = cmd, stdin, bufio.NewReader(stdout), &eb
	}
	gone := func(err error) {
		termSan.cmd = nil
		t.Fatalf("driver gone on input %q: %v\n%s", b, err, termSan.err.String())
	}
	if _, err := fmt.Fprintf(termSan.in, "%d\n%s", len(b), b); err != nil {
		gone(err)
	}
	line, err := termSan.out.ReadString('\n')
	if err != nil {
		gone(err)
	}
	line = strings.TrimRight(line, "\n")
	if line == "INVALID" {
		return "", false
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		gone(err)
	}
	o := make([]byte, n)
	if _, err := io.ReadFull(termSan.out, o); err != nil {
		gone(err)
	}
	return string(o), true
}

// termSanStop ends the driver.
func termSanStop() {
	termSan.Lock()
	defer termSan.Unlock()
	if termSan.cmd == nil {
		return
	}
	termSan.in.Close()
	_ = termSan.cmd.Wait()
	termSan.cmd = nil
}

func termSanCheck(t *testing.T, b []byte) {
	t.Helper()
	got, ok := termSanAsk(t, b)
	if !utf8.Valid(b) {
		if ok {
			t.Fatalf("input %q is not UTF-8, but the driver sanitized it", b)
		}
		return
	}
	if !ok {
		t.Fatalf("input %q is UTF-8, but the driver refused it", b)
	}
	if want := termSanitizeModel(string(b)); got != want {
		t.Fatalf("input %q:\n got %q\nwant %q", b, got, want)
	}
}

// termSanRecorded are hand-picked texts: escapes and controls at the
// vector steps' edges, C1 controls as C2 80 to C2 9F next to C2 A0 and
// C3 80, DEL, a lone C2 lead at the end, and runs around the 512-byte
// staging block.
var termSanRecorded = []string{
	"",
	"plain text",
	"\x1b[2J\x1b]0;title\x07",
	"ok\x1b[2J\x07\r\nnext\tline\x7f\u009b31m\u00e9\U0001F600",
	"0123456789abcde\x1b",
	"0123456789abcdef\x1b",
	"\u0080\u009f\u00a0\u00c0\u00ff",
	"\u00c2\u0080",
	"\x00\x01\x1f\x20\x7e\x7f",
	strings.Repeat("a", 509) + "\x1b",
	strings.Repeat("a", 510) + "\x1b",
	strings.Repeat("a", 511) + "\x1b" + strings.Repeat("b", 600) + "\x07",
	strings.Repeat("\x1bx", 300),
	strings.Repeat("\u00e9\u009b", 200),
}

// termSanGen is one generated text: pieces of the kinds above.
func termSanGen(r *rand.Rand) []byte {
	pieces := []string{"a", "bc", "\n", "\t", "\x1b", "\x00", "\x1f", "\x7f", "\u00e9", "\u009b", "\u0080",
		"\u00a0", "\u20ac", "0123456789abcdef", "\U0001F600", strings.Repeat("z", 520)}
	var b []byte
	n := r.Intn(300)
	for range n {
		k := r.Intn(len(pieces) * 4)
		if k >= len(pieces) {
			k %= len(pieces) - 1 // the long run is one piece in 64
		}
		b = append(b, pieces[k]...)
	}
	return b
}

// TestTerm_SanitizeOracle compares term.Sanitize with the rune model over
// the recorded texts, a few ill-formed ones, and 5,000 generated texts
// (seed 9).
func TestTerm_SanitizeOracle(t *testing.T) {
	t.Cleanup(termSanStop)
	for _, s := range termSanRecorded {
		termSanCheck(t, []byte(s))
	}
	for _, b := range [][]byte{{0xff}, {0xc2}, {'a', 0xc2}, {0xed, 0xa0, 0x80}} {
		termSanCheck(t, b)
	}
	r := rand.New(rand.NewSource(9))
	for range 5_000 {
		termSanCheck(t, termSanGen(r))
	}
}

// FuzzTerm_Sanitize is the fuzz target for term.Sanitize: each input,
// made valid UTF-8 (ill-formed bytes become U+FFFD), is sanitized by the
// Mk function in a long-running driver (a dev build, every check on); a
// panic, a hang or an output that differs from the model fails.
func FuzzTerm_Sanitize(f *testing.F) {
	for _, s := range termSanRecorded {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 16384 {
			b = b[:16384]
		}
		termSanCheck(t, []byte(strings.ToValidUTF8(string(b), "\uFFFD")))
	})
}
