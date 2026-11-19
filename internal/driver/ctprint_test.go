// Why: a function that forwards its const format to Print must run at compile
// time too; the evaluator has to run the instance specialised with the format.
// What: TestCompileTimeForwardedPrint over the shapes ctPrintFns,
// TestCompileTimeLoggerRefused for std.log's Logger in a const, and
// TestGeneratorForwardedPrint for a gen line that formats the same way.
// How: each shape's const, the evaluator's value, must equal the compiled
// program's value at run time, and both the digest of the bytes it writes.

package driver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// ctPrintFns are the shapes: a forwarder with no record (S1), a generic one
// unpadded (S2) and padded (S3), a forwarder of a forwarder (S4), a method
// forwarder over a Writer field (S5), into a Text (S6), an Arena.Fmt
// forwarder (S7), std.log's Logger (S8, at run time only: its const is
// refused, see TestCompileTimeLoggerRefused), and, as a control without a
// forwarder, a literal Print over a generic record field (S9); and a
// forwarder called with an aggregate const argument beside the format (S10),
// whose argument lower leaves in a temporary, so the call must be keyed by
// that temporary's value.
const ctPrintFns = `
Say0(w: mut Writer, const f: str): WriteError!void {
    try Print(w, f)
}

Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

Outer[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Say(w, f, args)
}

view struct Con {
    w: mut Writer
}

Con.Print[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try Print(mut *self.w, f, args)
}

AFmt[A: Fields[Format] + Plain](a: &Arena, const f: str, args: A): OutOfMemory!str from a {
    return try a.Fmt(f, args)
}

Show[T: Format + Plain](w: mut Writer, x: T): WriteError!void {
    try Print(w, "<{x}>", (x))
}

Digest(b: []u8): u64 {
    var h: u64 = 14695981039346656037
    for c in b {
        h = (h ^ u64(c)) *% 1099511628211
    }
    return h +% Cast[u64](b.Len())
}

S1(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    Say0(mut w, "plain {{}}\n") else {
        return 1
    }
    return Digest(c.Bytes())
}

S2(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    n := -42
    s := "str"
    Say(mut w, "{n}-{s}|{n:x}", (n, s)) else {
        return 1
    }
    return Digest(c.Bytes())
}

S3(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    n := 7
    s := "ab"
    Say(mut w, "{n:>5}|{s:*^7}|{n:+04}", (n, s)) else {
        return 1
    }
    return Digest(c.Bytes())
}

S4(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    f := 2.5
    Outer(mut w, "outer {f} {f:.3}", (f)) else {
        return 1
    }
    return Digest(c.Bytes())
}

S5(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    var con := Con(w: mut w)
    k := 300
    con.Print("con {k}", (k)) else {
        return 1
    }
    con.Print("done\n") else {
        return 2
    }
    return Digest(c.Bytes())
}

S6(a: &Arena): u64 {
    var t := Text.New(a)
    var w := t.Writer()
    n := 12345
    Say(mut w, "text {n} \u{e9}", (n)) else {
        return 1
    }
    return Digest(t.Str().AsBytes())
}

S7(a: &Arena): u64 {
    n := 99
    s := AFmt(a, "arena {n}|{n:>4}", (n)) else {
        return 1
    }
    return Digest(s.AsBytes())
}

S8(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    var l := log.Logger.New(mut w, log.Level.Info)
    k := 3
    l.Info("event {k}", (k)) else {
        return 1
    }
    l.Debug("hidden {k}", (k)) else {
        return 2
    }
    return Digest(c.Bytes())
}

struct Opts {
    export f: str
    export n: int
}

Inner(w: mut Writer, const f: str): WriteError!void {
    try Print(w, f)
}

Both(w: mut Writer, const o: Opts, const f: str): WriteError!void {
    try Inner(w, f)
}

S10(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    Both(mut w, Opts(f: "x", n: 2), "both {{}}") else {
        return 1
    }
    return Digest(c.Bytes())
}

S9(): u64 {
    var store: [64]u8 = [0; 64]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    Show(mut w, 5) else {
        return 1
    }
    Show(mut w, "s") else {
        return 2
    }
    return Digest(c.Bytes())
}

`

// ctPrintShapes are each shape's call (in a const and at run time) and the
// bytes it writes.
var ctPrintShapes = []struct {
	name, constArg, runArg, want string
}{
	{"S1", "", "", "plain {}\n"},
	{"S2", "", "", "-42-str|ffffffffffffffd6"},
	{"S3", "", "", "    7|**ab***|+007"},
	{"S4", "", "", "outer 2.5 2.500"},
	{"S5", "", "", "con 300done\n"},
	{"S6", "EvalArena()", "sys.arena", "text 12345 é"},
	{"S7", "EvalArena()", "sys.arena", "arena 99|  99"},
	{"S8", "", "", "INFO event 3\n"},
	{"S9", "", "", "<5><s>"},
	{"S10", "", "", "both {}"},
}

// ctDigest is the shapes' Digest: FNV-1a 64 over b plus its length.
func ctDigest(b string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(b); i++ {
		h = (h ^ uint64(b[i])) * 1099511628211
	}
	return h + uint64(len(b))
}

// ctRunOnly are the shapes with no const: the program prints their run-time
// value twice. S8's const is refused (TestCompileTimeLoggerRefused).
var ctRunOnly = map[string]bool{"S8": true}

// ctProgram is the shapes with one const per shape named and a Main that
// prints, per shape, its name, the run-time value and the const.
func ctProgram(shapes []int) string {
	var b strings.Builder
	b.WriteString("import std.log\nimport std.text\n")
	b.WriteString(ctPrintFns)
	for _, i := range shapes {
		s := ctPrintShapes[i]
		if !ctRunOnly[s.name] {
			fmt.Fprintf(&b, "const C%s: u64 = %s(%s)\n", s.name, s.name, s.constArg)
		}
	}
	b.WriteString("\nexport Main(sys: &System): WriteError!void {\n")
	for _, i := range shapes {
		s := ctPrintShapes[i]
		r := "r" + strings.ToLower(s.name)
		c, args := "C"+s.name, r+", C"+s.name
		if ctRunOnly[s.name] {
			c, args = r, r
		}
		fmt.Fprintf(&b, "    %s := %s(%s)\n    try sys.Print(\"%s {%s} {%s}\\n\", (%s))\n", r, s.name, s.runArg, s.name, r, c, args)
	}
	b.WriteString("}\n")
	return b.String()
}

// ctCheck builds and runs src under prof and checks every shape's line.
func ctCheck(t *testing.T, src string, prof types.Profile, shapes []int) {
	t.Helper()
	dir := writeTree(t, map[string]string{"main.mk": src})
	exe := buildProgram(t, filepath.Join(dir, "main.mk"), types.X86_64Windows, prof, t.TempDir())
	out, errOut, code := runExe(t, types.X86_64Windows, exe, "")
	if code != 0 {
		t.Fatalf("%v: exit %d\n%s", prof, code, errOut)
	}
	var want strings.Builder
	for _, i := range shapes {
		s := ctPrintShapes[i]
		d := ctDigest(s.want)
		fmt.Fprintf(&want, "%s %d %d\n", s.name, d, d)
	}
	if got := strings.ReplaceAll(out, "\r\n", "\n"); got != want.String() {
		t.Errorf("%v: stdout\n%s\nwant (name, run-time digest, const digest)\n%s", prof, got, want.String())
	}
}

// TestCompileTimeForwardedPrint: each shape alone in a dev build, so
// one shape's E0000 cannot hide another's, then every shape together in a
// release build.
func TestCompileTimeForwardedPrint(t *testing.T) {
	var all []int
	for i, s := range ctPrintShapes {
		all = append(all, i)
		t.Run(s.name, func(t *testing.T) {
			ctCheck(t, ctProgram([]int{i}), types.Dev, []int{i})
		})
	}
	t.Run("release", func(t *testing.T) {
		ctCheck(t, ctProgram(all), types.Release, all)
	})
}

// TestCompileTimeLoggerRefused: a Logger.Info call in a const is E0901,
// refused: io, though the Logger writes to memory. Compile-time code does
// no I/O, and the evaluator refuses every callee that takes an Io: a
// Logger formats each line into a Writer over a WritePortMut, whose Writer
// method and entries take one.
func TestCompileTimeLoggerRefused(t *testing.T) {
	src := "import std.log\nimport std.text\n" + ctPrintFns +
		"const CS8: u64 = S8()\n\nexport Main(sys: &System): WriteError!void {\n    try sys.Print(\"{CS8}\\n\", (CS8))\n}\n"
	dir := writeTree(t, map[string]string{"main.mk": src})
	s, err := NewSession(Config{Root: filepath.Join(dir, "main.mk"), Target: types.X86_64Windows, Profile: types.Dev, Tools: needTools(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(filepath.Join(t.TempDir(), "main.exe"))
	if err != nil {
		t.Fatal(err)
	}
	text := r.Report.Text(r.Sources)
	if len(r.Report.Diags) != 1 || !strings.Contains(text, "E0901") || !strings.Contains(text, "refused an operation: io") {
		t.Fatalf("want one E0901 refused: io, got %d diagnostics\n%s", len(r.Report.Diags), text)
	}
}

// TestGeneratorForwardedPrint: a generator body (a gen line, which the
// evaluator runs) that formats through a forwarded Print into a Capped and
// emits a const of the byte count.
func TestGeneratorForwardedPrint(t *testing.T) {
	tools := needTools(t)
	root := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"gencheck"`, "bins": `[pkg.Bin(name: "gencheck", root: "src/main", link_script: "", cpu: "", subsystem: "console")]`}),
		"src/genlib/genlib.mk": `import std.text

Say[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

export Count(g: mut Gen, t: Type) {
    var store: [32]u8 = [0; 32]
    var c := text.Capped.New(mut store[..])
    var w := c.Writer()
    n := 5
    Say(mut w, "n={n}|{n:>3}", (n)) else {
        g.FailOn(t, "write failed")
        return
    }
    if c.Bytes().Len() != 7 {
        g.FailOn(t, "wrong bytes")
        return
    }
    g.Emit(quote {
        const Made: int = 7
    })
}
`,
		"src/main/main.mk": `import src.genlib

struct P {
    x: int
}

gen genlib.Count(P)

export Main(sys: &System): WriteError!void {
    try sys.Print("{Made}\n", (Made))
}
`,
	})
	s, err := NewSession(Config{Root: root, Profile: types.Dev, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "gencheck.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	if out, _, code := runExe(t, types.X86_64Windows, paths[0], ""); strings.ReplaceAll(out, "\r\n", "\n") != "7\n" || code != 0 {
		t.Errorf("exit %d, stdout %q, want \"7\n\"", code, out)
	}
}
