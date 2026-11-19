// Why: a kernel prints through its own console Writer with no hosted std;
// that shape must link, and mk fix's short form of Print calls must be exact.
// What: TestFreestandingPrintLinks, TestFixWritesTheShortForm and
// TestFixShortFormEdges, over the programs freePrint, shortBefore,
// shortAfter, shortEdges and shortEdgesAfter.
// How: a freestanding console that owns its Writer and prints through a
// forwarding Print method, with widths, fills, signs and a radix, links for
// x86_64-freestanding in dev and release, and the padding runner and the
// counting Writer are in the image (a general-regs-only bin must not refuse
// an integer Print with E1108 because of FmtValue's float arms). Nothing
// here runs the ELF: printing it over serial needs a bootable kernel image
// (tests/freestanding/kernel and qemu_test.go). The fix tests compare mk
// fix's output with the expected text.

package driver

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

const freePrint = `import std.free

view struct Con {
    w: mut Writer
}

Con.Print[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try Print(mut *self.w, f, args)
}

Run(t: mut Text): WriteError!void {
    var w := t.Writer()
    var con := Con(w: mut w)
    n := 42
    x: i64 = -25
    h: u32 = 0xBEEF
    try con.Print("{n:>4}|{x:+08}|{h:x}|{h:#^10X}\n", (n, x, h))
    try con.Print("done\n")
}

export extern MkEntry() {
    var ar := Arena.New(free.Heap()) else {
        for {
        }
    }
    var t := Text.New(&ar)
    Run(mut t) else {
    }
    for {
    }
}
`

// TestFreestandingPrintLinks: the console program builds at dev and
// release, entered at MkEntry, and its image holds FmtPadRun and
// CountWrite, so a width in a kernel's format is printed, not refused.
func TestFreestandingPrintLinks(t *testing.T) {
	t.Parallel()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			file := filepath.Join(dir, "main.mk")
			if err := os.WriteFile(file, []byte(freePrint), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := buildProgram(t, file, types.X86_64Freestanding, prof, t.TempDir())
			f, err := elf.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if f.Type != elf.ET_EXEC || f.Machine != elf.EM_X86_64 {
				t.Errorf("type %v machine %v, want ET_EXEC EM_X86_64", f.Type, f.Machine)
			}
			syms, err := f.Symbols()
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]bool{}
			for _, s := range syms {
				byName[s.Name] = true
			}
			for _, want := range []string{"MkEntry", "mk.std.core.FmtPadRun", "mk.std.core.CountWrite"} {
				if !byName[want] {
					t.Errorf("no %s in the %v image", want, prof)
				}
			}
		})
	}
}

const shortBefore = `struct Con {
    n: int
}

Con.Say[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

export Main(sys: &System): WriteError!void {
    try sys.Print("a\n", ())
    try sys.PrintErr("b\n", ( ))
    var out := sys.Stdout()
    c := Con(n: 0)
    try c.Say(mut out, "c", ())
    try Print(mut out, "{{d}}\n", ())
    try sys.Print("{n}\n", (n: 1))
    try sys.Print("e\n", () // kept
    )
    try sys.Print("f\n")
}
`

const shortAfter = `struct Con {
    n: int
}

Con.Say[A: Fields[Format] + Plain](&self, w: mut Writer, const f: str, args: A): WriteError!void {
    try Print(w, f, args)
}

export Main(sys: &System): WriteError!void {
    try sys.Print("a\n")
    try sys.PrintErr("b\n")
    var out := sys.Stdout()
    c := Con(n: 0)
    try c.Say(mut out, "c")
    try Print(mut out, "{{d}}\n")
    try sys.Print("{n}\n", (n: 1))
    try sys.Print("e\n", () // kept
    )
    try sys.Print("f\n")
}
`

// TestFixWritesTheShortForm: mk fix, on a clean package, drops the
// written () of every call of a Print-shaped callee whose text has no hole
// (System.Print, System.PrintErr, a method of the user's, Print itself),
// keeps a record with fields, and leaves a call with a comment as written;
// the result checks clean and the round is reported as "short form".
func TestFixWritesTheShortForm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(p, []byte(shortBefore), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: p, CacheDir: filepath.Join(dir, ".cache")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Fix(FixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != shortAfter {
		t.Errorf("after mk fix:\n%s\nwant:\n%s", after, shortAfter)
	}
	if res.Stopped != "clean" || len(res.Final.Report.Diags) != 0 {
		t.Errorf("stopped %q with %d diagnostics", res.Stopped, len(res.Final.Report.Diags))
	}
	if n := len(res.Rounds); n != 1 || res.Rounds[0].Phase != "short form" || len(res.Rounds[0].Applied) != 4 {
		t.Errorf("rounds %+v, want one short form round of 4", res.Rounds)
	}
}

const shortEdges = `const Msg: str = "named"

export Main(sys: &System): WriteError!void {
    try sys.Print(
        "multi",
        ())
    try sys.Print(Msg, ())
    try sys.Print(
        "y", // why
        ())
}

test "t" (sys: &System) {
    try sys.Print("in test", ())
}
`

const shortEdgesAfter = `const Msg: str = "named"

export Main(sys: &System): WriteError!void {
    try sys.Print(
        "multi")
    try sys.Print(Msg, ())
    try sys.Print(
        "y", // why
        ())
}

test "t" (sys: &System) {
    try sys.Print("in test")
}
`

// TestFixShortFormEdges: a call over several lines is rewritten; a
// named-const text and a call with a comment inside are left as written;
// the round lists its edits in source order; and a second mk fix finds
// nothing to do (idempotence).
func TestFixShortFormEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(p, []byte(shortEdges), 0o644); err != nil {
		t.Fatal(err)
	}
	fix := func() *FixResult {
		s, err := NewSession(Config{Root: p, CacheDir: filepath.Join(dir, ".cache")})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Fix(FixOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := fix()
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != shortEdgesAfter {
		t.Errorf("after mk fix:\n%s\nwant:\n%s", after, shortEdgesAfter)
	}
	if len(res.Rounds) != 1 || len(res.Rounds[0].Applied) != 2 {
		t.Fatalf("rounds %+v, want one short form round of 2", res.Rounds)
	}
	if a, b := res.Rounds[0].Applied[0].At, res.Rounds[0].Applied[1].At; !strings.HasSuffix(a, ":4:9") || !strings.HasSuffix(b, ":14:9") {
		t.Errorf("edits at %s then %s, want 4:9 then 14:9", a, b)
	}
	if again := fix(); len(again.Rounds) != 0 || again.Stopped != "clean" {
		t.Errorf("second run: rounds %+v, stopped %q; want none, clean", again.Rounds, again.Stopped)
	}
}
