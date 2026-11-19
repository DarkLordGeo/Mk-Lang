// Why: text.Capped and text.Sink are the public Writers that need no Arena
// and no Io, so freestanding code can print before any heap exists.
// What: a freestanding image that prints with them before any heap and in
// its panic hook, and loan-rule tests when a user program makes them.
// How: builds at dev and release and wants no E1108; their behaviour is
// tested by std.text's blocks (text/tests_writer.mk).

package std

import (
	"debug/elf"
	"path/filepath"
	"strings"
	"testing"
)

const writerFreePkg = `const Package = pkg.Package(
    name: "probe",
    bins: [
        pkg.Bin(name: "probe", root: "src", link_script: "", cpu: "", subsystem: "console"),
    ],
    deps: [],
    c_sources: [],
    c_imports: [],
    asm_sources: [],
    targets: [],
    unsafe_modules: [],
    links: [],
)
`

// A device that counts bytes, a const Sink over it, a panic hook that
// prints the report with widths and a radix through a buffered Writer, and
// an entry that formats into a Capped on its stack and copies the bytes
// to the device through an unbuffered Writer. No Arena anywhere.
const writerFreeMain = `import std.free
import std.text

struct Port {
    sent: u64
}

Emit(p: mut Port, b: []u8): WriteError!void {
    p.sent += Cast[u64](b.Len())
}

const Com1 = text.Sink[Port].New(Emit)

export extern MkPanicHook(info: &free.PanicInfo): never {
    var port := Port(sent: 0)
    var buf: [64]u8 = [0; 64]
    var w := (&Com1).Writer(mut port, mut buf[..])
    code := info.code
    line := info.line
    Print(mut w, "\npanic: {m} (code {code}) at {f}:{line:>4}|{h:x}\n", (m: info.message, code, f: info.file, line, h: u32(0xBEEF))) else {
    }
    w.Flush() else {
    }
    for {
    }
}

export extern MkEntry() {
    var store: [32]u8 = [0; 32]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    n := 42
    x: i64 = -25
    Print(mut w, "{n:>4}|{x:+08}\n", (n, x)) else {
    }
    var port := Port(sent: 0)
    var empty: [0]u8 = []
    var w2 := (&Com1).Writer(mut port, mut empty[..])
    w2.Bytes(c.Bytes()) else {
    }
    for {
    }
}
`

// TestFreestandingWriters: the program builds for
// x86_64-freestanding (a general-regs-only bin, the target's default cpu)
// in dev and release with no diagnostic, so no E1108, and its image holds
// the Sink's and the Capped's write entries and the padding runner.
func TestFreestandingWriters(t *testing.T) {
	files := map[string]string{"pkg.mk": writerFreePkg, "src/main.mk": writerFreeMain}
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			args := []string{"build", "--target", "x86_64-freestanding", "-o", filepath.Join(t.TempDir(), "probe")}
			if prof == "release" {
				args = append(args, "--release")
			}
			out, status := dfRun(t, files, args...)
			if status != 0 || strings.Contains(out, "E1108") || strings.Contains(out, ": E") {
				t.Fatalf("mk %s: exit %d\n%s", strings.Join(args, " "), status, out)
			}
			f, err := elf.Open(args[4])
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			syms, err := f.Symbols()
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"MkPanicHook", "mk.std.core.SinkWrite", "mk.std.core.CappedWrite", "mk.std.core.FmtPadRun"} {
				found := false
				for _, s := range syms {
					if s.Name == want || strings.HasPrefix(s.Name, want+".") {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s: no symbol %s in the image", prof, want)
				}
			}
		})
	}
}

const writerLoanHeader = `import std.text

struct Dev {
    n: int
}

Put(d: mut Dev, b: []u8): WriteError!void {
    d.n += b.Len()
}

`

// TestLoanRejects: each program misuses a Writer from
// text.Capped or text.Sink, and mk check rejects it with the one
// diagnostic named; the control, the same shapes used in order, checks
// clean.
func TestLoanRejects(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
	}{
		{"a Capped moved, then its Writer made", `export Main(sys: &System): WriteError!void {
    var b: [8]u8 = [0; 8]
    var c := text.Capped.New(mut b[..])
    var d := c
    var w := c.Writer()
    try w.Str("abc")
    d.Reset()
}`, "main.mk:15:14: E0401"},
		{"a Writer returned past its Capped", `Make(): Writer {
    var b: [8]u8 = [0; 8]
    var c := text.Capped.New(mut b[..])
    return c.Writer()
}

export Main(sys: &System): WriteError!void {
    var w := Make()
    try w.Str("abc")
}`, "main.mk:14:5: E0603"},
		{"the Sink's state written while its Writer lives", `export Main(sys: &System): WriteError!void {
    s := text.Sink[Dev].New(Put)
    var d := Dev(n: 0)
    var b: [8]u8 = [0; 8]
    var w := s.Writer(mut d, mut b[..])
    d.n = 5
    try w.Str("abc")
}`, "main.mk:16:5: E0502"},
		{"two Writers over one state", `export Main(sys: &System): WriteError!void {
    s := text.Sink[Dev].New(Put)
    var d := Dev(n: 0)
    var e: [0]u8 = []
    var f: [0]u8 = []
    var w := s.Writer(mut d, mut e[..])
    var x := s.Writer(mut d, mut f[..])
    try w.Str("abc")
    try x.Str("abc")
}`, "main.mk:17:23: E0501"},
		{"the Sink reassigned while its Writer lives", `Other(d: mut Dev, b: []u8): WriteError!void {
}

export Main(sys: &System): WriteError!void {
    var s := text.Sink[Dev].New(Put)
    var d := Dev(n: 0)
    var e: [0]u8 = []
    var w := s.Writer(mut d, mut e[..])
    s = text.Sink[Dev].New(Other)
    try w.Str("abc")
}`, "main.mk:19:5: E0502"},
		{"a Capped read while its Writer lives", `export Main(sys: &System): WriteError!void {
    var b: [8]u8 = [0; 8]
    var c := text.Capped.New(mut b[..])
    var w := c.Writer()
    k := c.Bytes()
    try w.Str("abc")
    _ = k
}`, "main.mk:15:10: E0504"},
		{"the buffer written while the Writer lives", `export Main(sys: &System): WriteError!void {
    s := text.Sink[Dev].New(Put)
    var d := Dev(n: 0)
    var b: [8]u8 = [0; 8]
    var w := s.Writer(mut d, mut b[..])
    b[0] = 1
    try w.Str("abc")
}`, "main.mk:16:5: E0502"},
		// A Writer hands write a slice of its own buffer (Writer.Drain), so a
		// write that kept its run in S would read the buffer after it dies:
		// Sink's fn type has no `into`, so such a write is refused where it
		// is given (E0608; with no clause it is E0604 at its body).
		{"a write function that keeps its run in the state", `view struct Keep {
    last: []u8
}

KeepPut(k: mut Keep, b: []u8 into k): WriteError!void {
    k.last = b
}

export Main(sys: &System): WriteError!void {
    s := text.Sink[Keep].New(KeepPut)
    var k := Keep(last: "init".AsBytes())
    var b: [8]u8 = [0; 8]
    var w := s.Writer(mut k, mut b[..])
    try w.Str("abc")
}`, "main.mk:20:30: E0608"},
		{"a Capped returned past its buffer", `Make(): text.Capped {
    var store: [16]u8 = [0; 16]
    return text.Capped.New(mut store[..])
}

export Main(sys: &System): WriteError!void {
    var c := Make()
    var w := c.Writer()
    try w.Str("abc")
}`, "main.mk:13:5: E0603"},
		{"a Capped reset while its Bytes are held", `export Main(sys: &System): WriteError!void {
    var store: [8]u8 = [0; 8]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    try w.Str("AAAA")
    k := c.Bytes()
    c.Reset()
    Assert(k.Len() == 4)
}`, "main.mk:17:5: E0501"},
		{"a Capped moved while its Bytes are held", `export Main(sys: &System): WriteError!void {
    var store: [8]u8 = [0; 8]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    try w.Str("AAAA")
    k := c.Bytes()
    var d := c
    d.Reset()
    Assert(k.Len() == 4)
}`, "main.mk:17:5: E0503"},
		{"control", `export Main(sys: &System): WriteError!void {
    s := text.Sink[Dev].New(Put)
    var d := Dev(n: 0)
    var b: [8]u8 = [0; 8]
    var w := s.Writer(mut d, mut b[..])
    try w.Str("abc")
    try w.Flush()
    d.n += 1
    var store: [8]u8 = [0; 8]
    var c := text.Capped.New(mut store[..])
    var cw := c.Writer()
    try cw.Str("abc")
    Assert(c.Bytes().Len() == 3 && d.n == 4)
}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, status := dfRun(t, map[string]string{"main.mk": writerLoanHeader + c.body}, "check", "main.mk")
			if c.want == "" {
				if status != 0 {
					t.Fatalf("mk check: exit %d\n%s", status, out)
				}
				return
			}
			if status == 0 || !strings.Contains(out, c.want) || strings.Count(out, ": E") != 1 {
				t.Fatalf("mk check: exit %d, want the one diagnostic %s\n%s", status, c.want, out)
			}
		})
	}
}
