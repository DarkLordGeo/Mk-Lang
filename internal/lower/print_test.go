// Why: a Print hole may name a field path and carry a spec, and lower picks
// the runner and value functions each call needs.
// What: TestPrintFieldPath, TestRunnerChoice.
// How: lowers Print calls and checks the argument array and the functions
// called.

package lower

import (
	"strings"
	"testing"
)

// TestPrintFieldPath: a field path's FmtArg points into the record
// temporary through a field projection; a top-level field used only by
// paths gets no FmtArg of its own, and a path used twice gets one.
func TestPrintFieldPath(t *testing.T) {
	src := `struct P {
	export x: int
	export y: int
}

impl Format for P {
	Format(&self, w: mut Writer): WriteError!void {
	}
}

One(w: mut Writer, p: P): WriteError!void {
	try Print(w, "{p.y}{p.x}{p.y}", (p))
}
`
	got := lowerFmt(t, src).print("One")
	if strings.Contains(got, "FmtValue[P]") {
		t.Fatalf("a field used only by paths is formatted:\n%s", got)
	}
	// Two addresses (p.y once, p.x), each a field projection of the record
	// temporary _5 (field 0 is p, a P: y is its field 1, x its field 0).
	if n := strings.Count(got, "call std.raw.Of[int]("); n != 2 {
		t.Fatalf("want 2 field addresses (p.y once, p.x), got %d:\n%s", n, got)
	}
	// The holes call FmtValue[int] on entries 0, 1, 0: p.y, p.x, p.y, with
	// no literal between them (each is empty).
	wantIn(t, got, "= &_5.0.1", "_8 = call std.raw.PtrCast[int, u8]", "= &_5.0.0", "_11 = call std.raw.PtrCast[int, u8]",
		"call std.core.FmtValue[int](copy _8, u32 0", "call std.core.FmtValue[int](copy _11, u32 0", "call std.core.FmtValue[int](copy _8, u32 0")
	if strings.Contains(got, "Writer:Str") || strings.Contains(got, "FmtRun") {
		t.Fatalf("empty literals are written, or the runner is called:\n%s", got)
	}
}

// TestRunnerChoice: a call with no width runs through FmtRun (or
// FmtArena) and one with a width through FmtPadRun (FmtArenaPad), so a
// program that pads nothing links no padding code; an entry takes
// FmtSpecValue only when a valued spec reads it, and the spec words are
// fmtspec.Encode's.
func TestRunnerChoice(t *testing.T) {
	src := `Flat(w: mut Writer, n: int, s: str): WriteError!void {
	try Print(w, "{n} {s}", (n, s))
}
Radix(w: mut Writer, n: int): WriteError!void {
	try Print(w, "{n} {n:X}", (n))
}
Pad(w: mut Writer, n: int, s: str): WriteError!void {
	try Print(w, "{n} {s:*^9}", (n, s))
}
Zero(w: mut Writer, n: int): WriteError!void {
	try Print(w, "{n:+08}", (n))
}
ArenaPlain(a: &Arena, n: int): OutOfMemory!str from a {
	s := try a.Fmt("{n:x}", (n))
	return s
}
ArenaPad(a: &Arena, n: int): OutOfMemory!str from a {
	s := try a.Fmt("{n:<4}", (n))
	return s
}
`
	l := lowerFmt(t, src)
	for _, c := range []struct {
		fn       string
		run      string // "" for a straight-line Print (format.go)
		want     []string
		notValue string
	}{
		{"Flat", "", []string{"call std.core.FmtValue[int](copy _", ", u32 0, ", "call std.core.FmtValue[str](copy _"}, "FmtSpecValue"},
		// {n} beside {n:X}: one entry, which a valued spec reads, so both
		// holes take FmtSpecValue, the plain one with spec 0.
		{"Radix", "", []string{"call std.core.FmtSpecValue[int](copy _8, u32 0, ", `const str " "`, "call std.core.FmtSpecValue[int](copy _8, u32 3, "}, "FmtValue[int]"},
		// *^9 on a str: center (2 << 6), width 9 << 16, fill '*' << 24.
		{"Pad", "FmtPadRun", []string{"fn std.core.FmtValue[int]", "fn std.core.FmtValue[str]", `" ", i32 1, u32 705233024)`}, "FmtSpecValue"},
		// +08: sign 16, zero 32, right 1 << 6, width 8 << 16, fill ' '.
		{"Zero", "FmtPadRun", []string{"fn std.core.FmtSpecValue[int]", "i32 0, u32 537395312)"}, "FmtValue[int]"},
		{"ArenaPlain", "FmtArena", []string{"fn std.core.FmtSpecValue[int]"}, "FmtValue[int]"},
		{"ArenaPad", "FmtArenaPad", []string{"fn std.core.FmtValue[int]"}, "FmtSpecValue"},
	} {
		got := l.print(c.fn)
		if c.run != "" {
			wantIn(t, got, append(c.want, "call std.core."+c.run+"(")...)
		} else {
			wantIn(t, got, c.want...)
			if strings.Contains(got, "FmtRun") || strings.Contains(got, "FmtPiece") {
				t.Errorf("%s: an unpadded Print calls the runner:\n%s", c.fn, got)
			}
		}
		if strings.Contains(got, c.notValue) {
			t.Errorf("%s: names %s:\n%s", c.fn, c.notValue, got)
		}
		if c.run == "" && strings.Contains(got, "FmtPadRun") || c.run == "FmtArena" && strings.Contains(got, "FmtArenaPad") {
			t.Errorf("%s: a call with no width runs through the padding runner:\n%s", c.fn, got)
		}
	}
}
