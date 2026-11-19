// Why: the corpus judges units through the driver, so a build E0001 must
// reach a unit's Report like any other diagnostic, and the corpus's layout
// checks need check's own types.Layout.
// What: TestCheckUnitKeepsBuildE0001 and TestLayoutOf.
// How: builds a unit with LLVMDir naming a directory without clang, and
// queries Session.LayoutOf for root-module types before and after Check.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestCheckUnitKeepsBuildE0001: with LLVMDir naming a directory
// without clang, a clean unit built under UnitBuild has exactly one E0001
// in its Report (the build's "no usable LLVM toolchain"), even though
// E0001's phase (native) is past Through (flow), and nothing is built.
// Needs no LLVM.
func TestCheckUnitKeepsBuildE0001(t *testing.T) {
	c, err := NewCorpusSession(Config{LLVMDir: t.TempDir(), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.Through() != diag.PhaseFlow {
		t.Fatalf("Through %v, want flow\n%s", c.Through(), c.Std().Report.Text(c.Std().Sources))
	}
	for _, mode := range []UnitMode{UnitBuild, UnitRun} {
		out := c.CheckUnit(UnitInput{Name: "a1/" + mode.String(), Files: []UnitFile{{"block.mk", "F(): int {\n    return 1\n}\n"}}, Mode: mode})
		got := codesOf(out.Report)
		if !slices.Equal(got, []string{"E0001"}) || out.Built || out.Ran {
			t.Fatalf("%s: codes %q, Built %v, Ran %v; want one E0001, nothing built\n%s", mode, got, out.Built, out.Ran, out.Report.Text(out.Sources))
		}
		if d := out.Report.Diags[0].Diag; !strings.HasPrefix(d.Message, "no usable LLVM toolchain") {
			t.Errorf("%s: E0001 message %q, want the toolchain's", mode, d.Message)
		}
	}
	// UnitCheck builds nothing, so it has no E0001.
	out := c.CheckUnit(UnitInput{Name: "a1/check", Files: []UnitFile{{"block.mk", "F(): int {\n    return 1\n}\n"}}})
	if len(out.Report.Diags) != 0 {
		t.Errorf("UnitCheck: %s", out.Report.Text(out.Sources))
	}
}

const layoutSrc = `struct Pair {
    a: u8
    b: i64
}

packed struct P {
    a: u8
    b: u32
}

enum Tok {
    Num(n: u16)
    Ch(c: u8)
}

struct Box[T] {
    v: T
}

type Arr = [3]u16

type Opt = ?i32

F(): int {
    return 1
}
`

// TestLayoutOf: Session.LayoutOf gives check's layout of a struct,
// a packed struct, an enum and aliases of the root module, and is an error,
// never a panic, before Check, for an unknown name, a generic struct, a
// function, and an alias that did not check.
func TestLayoutOf(t *testing.T) {
	file := filepath.Join(t.TempDir(), "l.mk")
	if err := os.WriteFile(file, []byte(layoutSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: file, Target: types.X86_64Linux, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LayoutOf("Pair"); err == nil || !strings.Contains(err.Error(), "no Check") {
		t.Errorf("before Check: %v, want an error naming no Check", err)
	}
	if r := s.Check(); len(r.Report.Diags) > 0 {
		t.Fatalf("%s", r.Report.Text(r.Sources))
	}
	offsets := func(l types.Layout) []uint64 {
		var o []uint64
		for _, f := range l.Fields {
			o = append(o, f.Offset)
		}
		return o
	}
	for _, c := range []struct {
		name        string
		kind        types.TypeKind
		size, align uint64
		offsets     []uint64
		len         uint64
	}{
		{"Pair", types.KNamed, 16, 8, []uint64{0, 8}, 0},
		{"P", types.KNamed, 5, 1, []uint64{0, 1}, 0},
		{"Arr", types.KArray, 6, 2, nil, 3},
	} {
		l, err := s.LayoutOf(c.name)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if l.Kind != c.kind || l.Layout.Shape.Size != c.size || uint64(l.Layout.Shape.Align) != c.align || !slices.Equal(offsets(l.Layout), c.offsets) || l.Len != c.len {
			t.Errorf("%s: kind %v size %d align %d offsets %v len %d; want %v %d %d %v %d", c.name, l.Kind, l.Layout.Shape.Size, l.Layout.Shape.Align, offsets(l.Layout), l.Len, c.kind, c.size, c.align, c.offsets, c.len)
		}
	}
	if l, err := s.LayoutOf("Tok"); err != nil || !l.Layout.HasTag || l.Layout.Tag.Offset != 0 || len(l.Layout.Variants) != 2 ||
		l.Layout.Variants[0].Fields[0].Offset != 2 || l.Layout.Variants[1].Fields[0].Offset != 2 || l.Layout.Shape.Size != 4 {
		t.Errorf("Tok: %+v, %v; want tag at 0 and both payloads at 2, size 4", l, err)
	}
	if l, err := s.LayoutOf("Opt"); err != nil || l.Kind != types.KOptional || !l.Layout.HasTag || l.Layout.Tag.Niche || l.Layout.Variants[1].Fields[0].Offset != 4 {
		t.Errorf("Opt: %+v, %v; want a some byte at 0 and the value at 4", l, err)
	}
	for name, want := range map[string]string{
		"Nope": "declares no Nope",
		"Box":  "generic",
		"F":    "not a struct",
	} {
		if _, err := s.LayoutOf(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error with %q", name, err, want)
		}
	}
	// A names error stops the build before stage 5, so no layout exists.
	if err := os.WriteFile(file, []byte(layoutSrc+"\ntype Bad = Nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := s.Check(); !slices.Equal(codesOf(r.Report), []string{"E0201"}) {
		t.Fatalf("codes %q, want the one E0201 of Bad\n%s", codesOf(r.Report), r.Report.Text(r.Sources))
	}
	if _, err := s.LayoutOf("Pair"); err == nil || !strings.Contains(err.Error(), "before the types stage") {
		t.Errorf("after a names error: %v, want an error naming the types stage", err)
	}
}
