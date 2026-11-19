// Why: an unpadded Print is lowered straight-line (internal/lower/format.go)
// instead of through the runner, and its behaviour must stay the runner's.
// What: TestPrintStraightLine_SameBehaviour, its generated program
// (printDiffProgram) and testdata/printdiff.golden, the runner's output.
// How: formats every layout of literals and up to four holes on recording,
// buffered and failing Writers, in dev and release, against the golden.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// TestPrintStraightLine_SameBehaviour: the generated program formats every
// layout of empty and non-empty literals around up to four holes, on an
// unbuffered and a 3-byte-buffered recording Writer, on one that fails at
// every byte position, with a Format that fails at every hole and one that
// flushes its Writer at every hole; every spec kind with and without
// padding, field paths, escaped braces, a forwarder of a const text, the
// arguments' single evaluation before any output, a Print in an else
// handler over the same Writer, and a compile-time render compared with the
// run-time one. Its output in dev and in release must equal
// testdata/printdiff.golden, which a compiler that formats every Print
// through the runner printed in dev and release.
func TestPrintStraightLine_SameBehaviour(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(file, []byte(printDiffProgram()), 0o644); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "printdiff.golden")
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		exe := buildProgram(t, file, types.X86_64Windows, prof, t.TempDir())
		out, errOut, code := runExe(t, types.X86_64Windows, exe, "")
		out = strings.ReplaceAll(out, "\r\n", "\n")
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", prof, code, errOut)
		}
		if *update && prof == types.Dev {
			if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatal(err)
		}
		if out != string(want) {
			ol, wl := strings.Split(out, "\n"), strings.Split(string(want), "\n")
			for i := 0; i < len(ol) && i < len(wl); i++ {
				if ol[i] != wl[i] {
					t.Fatalf("%v: line %d is %q, want %q", prof, i+1, ol[i], wl[i])
				}
			}
			t.Fatalf("%v: %d lines, want %d", prof, len(ol), len(wl))
		}
	}
}

// printDiffProgram is the program: one function per layout, Specs, and a
// Main that runs them all.
func printDiffProgram() string {
	var b strings.Builder
	b.WriteString(printDiffHeader)
	lits := []string{"", "a", "{{", "xy}}", "lit-"}
	names := []string{"p", "q", "r", "s"}
	n := 0
	var calls []string
	for k := 0; k <= 4; k++ {
		for mask := 0; mask < 1<<(k+1); mask++ {
			var f strings.Builder
			for i := 0; i <= k; i++ {
				if mask&(1<<i) != 0 {
					f.WriteString(lits[1+(i+mask)%4])
				}
				if i < k {
					f.WriteString("{" + names[i] + "}")
				}
			}
			text := f.String() // mask 0 of k 0 is the empty text: no piece at all
			args := "()"
			if k > 0 {
				args = "(" + strings.Join(names[:k], ", ") + ")"
			}
			fmt.Fprintf(&b, "\nL%d(w: mut Writer, p: Tag, q: Tag, r: Tag, s: Tag): WriteError!void {\n", n)
			fmt.Fprintf(&b, "    try Print(w, %q, %s)\n}\n", text, args)
			calls = append(calls, fmt.Sprintf("    try Layout(sys, %d, %d, %q, L%d)\n", n, k, text, n))
			n++
		}
	}
	specs := []string{
		"{n}", "{n:x}", "{n:X}", "{n:b}", "{n:o}", "{n:+}", "{n:08}", "{n:+08}", "{n:>6}", "{n:<6}|", "{n:^7}|",
		"{n:*^9}", "{n:#>10x}", "{n:<8b}|", "{m}", "{m:x}", "{m:+}", "{m:012}", "{m:>24}", "{u}", "{u:X}", "{u:b}",
		"{v}", "{v:x}", "{v:+05}", "{c}", "{c:x}", "{c:08b}", "{f}", "{f:.3}", "{f:+.2}", "{f:010.3}", "{f:>12.1}",
		"{g}", "{g:.0}", "{h}", "{h:.4}", "{z}", "{z:+}", "{nan}", "{nan:08}", "{inf:+}", "{inf:09}", "{s}", "{s:>8}", "{s:-<8}|",
		"{s:*^9}", "{t}", "{t:>12}", "{t:_<11}|", "{e}", "{b}", "{b:>6}", "{p.x}/{p.y}", "{p.y:x}-{p.x:+}", "{n}{n:x}{n:X}",
		"{{{n}}}", "}}{{", "a{{b}}c{n}d{{", "{s}{t}{n}",
	}
	b.WriteString("\nSpecs(w: mut Writer, n: int, m: i64, u: u64, v: i32, c: u8, f: f64, g: f32, h: f64, z: int, nan: f64, inf: f64, s: str, t: Tag, e: Err, bo: bool, p: Pt): WriteError!void {\n")
	b.WriteString("    b := bo\n")
	for _, s := range specs {
		var fields []string
		seen := map[string]bool{}
		for _, h := range printDiffHoles(s) {
			if !seen[h] {
				seen[h] = true
				fields = append(fields, h)
			}
		}
		args := "()"
		if len(fields) > 0 {
			args = "(" + strings.Join(fields, ", ") + ")"
		}
		fmt.Fprintf(&b, "    try Print(w, %q, %s)\n    try w.Str(\"\\n\")\n", s, args)
	}
	b.WriteString("}\n")
	b.WriteString("\nexport Main(sys: &System): WriteError!void {\n")
	for _, c := range calls {
		b.WriteString(c)
	}
	b.WriteString(printDiffTail)
	return b.String()
}

// printDiffHoles are the names (paths cut at the dot) of a text's holes.
func printDiffHoles(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			if i+1 < len(s) && s[i+1] == '{' {
				i++
				continue
			}
			j := strings.IndexByte(s[i:], '}')
			h := s[i+1 : i+j]
			if c := strings.IndexByte(h, ':'); c >= 0 {
				h = h[:c]
			}
			if d := strings.IndexByte(h, '.'); d >= 0 {
				h = h[:d]
			}
			out = append(out, h)
			i += j
		} else if s[i] == '}' {
			i++
		}
	}
	return out
}

const printDiffHeader = `// Generated by internal/driver/printdiff_test.go: its output must equal
// testdata/printdiff.golden, the runner's, in dev and in release.
import std.text

struct Rec {
    log: [4096]u8
    n: int
    total: int
    calls: int
    limit: int
}

// RecWrite records each table call as |bytes, and fails when the run would
// take the total past limit (limit -1: never).
RecWrite(r: mut Rec, b: []u8): WriteError!void {
    r.calls += 1
    if r.limit >= 0 && r.total + b.Len() > r.limit {
        fail WriteError
    }
    r.total += b.Len()
    if r.n + 1 + b.Len() > 4096 {
        return
    }
    r.log[r.n] = 0x7C
    r.n += 1
    for x in b {
        r.log[r.n] = x
        r.n += 1
    }
}

const RecSink = text.Sink[Rec].New(RecWrite)

NewRec(limit: int): Rec {
    return Rec(log: [0; 4096], n: 0, total: 0, calls: 0, limit: limit)
}

// Tag's Format writes <id>, and with flush set flushes its Writer in the
// middle; with bad set it fails after "<id" (a Format that fails midway).
struct Tag {
    export id: int
    export bad: bool
    export flush: bool
}

impl Format for Tag {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("<")
        try self.id.Format(w)
        if self.flush {
            try w.Flush()
        }
        if self.bad {
            fail WriteError
        }
        try w.Str(">")
    }
}

struct Pt {
    export x: int
    export y: u16
}

error Err

T(id: int, bad: int, flush: int): Tag {
    return Tag(id: id, bad: bad == id, flush: flush == id)
}

Show(sys: &System, name: str, r: &Rec, ok: bool): WriteError!void {
    log := r.log[0..r.n]
    var res := "ok"
    if !ok {
        res = "ERR"
    }
    calls := r.calls
    total := r.total
    try sys.Print("{name} {res} calls={calls} total={total} [", (name, res, calls, total))
    var out := sys.Stdout()
    try out.Bytes(log)
    try sys.Print("]\n")
}

// RunOne runs f on a fresh sink of the given buffer size and limit, with
// the hole Tags 1..4, bad and flush as given; then flushes what is left.
RunOne(sys: &System, name: str, cap: int, limit: int, bad: int, flush: int, f: fn(mut Writer, Tag, Tag, Tag, Tag): WriteError!void): WriteError!void {
    var r := NewRec(limit)
    var buf: [8]u8 = [0; 8]
    var w := (&RecSink).Writer(mut r, mut buf[0..cap])
    var ok := true
    f(mut w, T(1, bad, flush), T(2, bad, flush), T(3, bad, flush), T(4, bad, flush)) else {
        ok = false
    }
    w.Flush() else {
        ok = false
    }
    try Show(sys, name, &r, ok)
}

Layout(sys: &System, i: int, k: int, text: str, f: fn(mut Writer, Tag, Tag, Tag, Tag): WriteError!void): WriteError!void {
    try sys.Print("L{i} k={k} \"{text}\"\n", (i, k, text))
    try RunOne(sys, "  plain", 0, -1, 0, 0, f)
    try RunOne(sys, "  buf3", 3, -1, 0, 0, f)
    for lim in 0..24 {
        try RunOne(sys, "  lim", 0, lim, 0, 0, f)
        try RunOne(sys, "  limbuf", 3, lim, 0, 0, f)
    }
    for j in 1..(k + 1) {
        try RunOne(sys, "  bad", 0, -1, j, 0, f)
        try RunOne(sys, "  flush", 8, -1, 0, j, f)
        try RunOne(sys, "  badbuf", 8, -1, j, 0, f)
    }
}

// Say forwards its const text: mono expands the Print per instance.
Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

// Next counts its calls: each record field is evaluated once, before any
// output, whatever the Print writes or where it fails.
Next(c: mut int): int {
    *c += 1
    return *c
}

// Render formats into a Capped over a local array (no Io, so it runs at
// compile time too) and digests the bytes.
Render(): u64 {
    var store: [1024]u8 = [0; 1024]
    var cp := text.Capped.New(mut store[..])
    var w := cp.Writer()
    n := 255
    x: i64 = -1234567
    f := 3.25
    s := "str"
    t := Tag(id: 9, bad: false, flush: false)
    Print(mut w, "a{n}b{x}c{f}d{s}e{t}f", (n, x, f, s, t)) else {
        return 1
    }
    Print(mut w, "{n:x}|{x:+}|{f:.1}|{s:>5}|{t:>6}|{{}}\n", (n, x, f, s, t)) else {
        return 2
    }
    Print(mut w, "", ()) else {
        return 3
    }
    Print(mut w, "plain\n") else {
        return 4
    }
    var h: u64 = 14695981039346656037
    for c in cp.Bytes() {
        h = (h ^ u64(c)) *% 1099511628211
    }
    return h +% Cast[u64](cp.Bytes().Len())
}

const Rendered: u64 = Render()
`

const printDiffTail = `    var r := NewRec(-1)
    var buf: [8]u8 = [0; 8]
    var w := (&RecSink).Writer(mut r, mut buf[0..5])
    var zf := 0.0
    if sys.args.Len() > 5 {
        zf = 1.0
    }
    try Specs(mut w, 300, -9223372036854775807 - 1, 18446744073709551615, -42, 200, 2.5, 0.1, -zf, 0, zf / zf, 1.0 / zf, "hey", Tag(id: 7, bad: false, flush: false), Err, true, Pt(x: -3, y: 65535))
    try w.Flush()
    try Show(sys, "specs", &r, true)
    // A const-text forwarder, padded and not, failing and not.
    var r2 := NewRec(10)
    var w2 := (&RecSink).Writer(mut r2, mut buf[0..0])
    var c := 0
    a := Tag(id: 5, bad: false, flush: false)
    var ok := true
    Say(mut w2, "fwd{a}|{n}|", (a, n: Next(mut c))) else {
        ok = false
    }
    Say(mut w2, "{a:>9}{n:x}", (a, n: Next(mut c))) else {
        ok = false
    }
    try Show(sys, "say", &r2, ok)
    // A forwarded straight-line Print with valued specs (FmtSpecValue on
    // the entries they read, a plain hole on a shared entry), and the same
    // forwarder failing partway.
    var r5 := NewRec(-1)
    var w5 := (&RecSink).Writer(mut r5, mut buf[0..0])
    hx := 255
    mm := -7
    fl := 2.5
    var ok5 := true
    Say(mut w5, "v{hx:x}|{mm:+}|{hx:o}|{fl:.2}|{hx}|{mm:X}", (hx, mm, fl)) else {
        ok5 = false
    }
    try Show(sys, "sayspec", &r5, ok5)
    var r6 := NewRec(6)
    var w6 := (&RecSink).Writer(mut r6, mut buf[0..0])
    var ok6 := true
    Say(mut w6, "abc{hx:x}def{mm}", (hx, mm)) else {
        ok6 = false
    }
    try Show(sys, "sayfail", &r6, ok6)
    // Arguments are evaluated once, before any output, even when the first
    // write fails.
    var r3 := NewRec(0)
    var w3 := (&RecSink).Writer(mut r3, mut buf[0..0])
    var cnt := 0
    Print(mut w3, "x{a}{b}{c}", (a: Next(mut cnt), b: Next(mut cnt), c: Next(mut cnt))) else {
        // The writer is usable again in the else handler.
        w3.Str("E") else {
        }
    }
    try sys.Print("count {cnt}\n", (cnt))
    try Show(sys, "evalonce", &r3, false)
    // A Print in an else handler, over the same local Writer.
    var r4 := NewRec(3)
    var w4 := (&RecSink).Writer(mut r4, mut buf[0..0])
    Print(mut w4, "abcd{cnt}", (cnt)) else {

        Print(mut w4, "<{cnt}>", (cnt)) else {
        }
    }
    try Show(sys, "else", &r4, true)
    rt := Render()
    try sys.Print("render {rt} const {Rendered}\n", (rt, Rendered))
    try sys.Print("done\n")
}
`
