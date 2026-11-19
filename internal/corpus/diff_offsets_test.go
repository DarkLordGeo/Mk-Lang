// Why: a golden field offset is only trusted once native code agrees with it.
// What: layoutNativeOffsets, the probes of every golden row, and
// TestLayoutOffsetSource.
// How: a probe writes a mark at the golden offset through raw pointers and
// reads the part back typed; a wrong offset makes the read differ.
//
// Every offset of layout.<target>.golden is checked by a native run on the two
// runnable targets, in dev and release, beside check's types.Layout
// (TestLayoutOffsetsChecked).
//
// Mk has no OffsetOf, and user code cannot import std.raw, so the
// program runs over a copy of the repository's std with one module added,
// std/zzlayout (the TestDiffFloatDigits technique): Put writes bytes inside
// a value through raw pointers. Each golden offset has a probe: a fresh
// value of the row's type, a distinct mark written at the golden offset
// with the width of the part, and a typed read of that part that holds only
// if the write landed on it (the field equals the mark, the optional became
// none, the result became ok, the enum's variant changed). A wrong golden
// offset writes into padding or into another part, and the read differs.
// Only typed reads are made, so no padding byte is ever read.
//
// The result rows (Bad!i64, Bad!u8, Big!u8) have no native probe: an E!T
// cannot be held in a local (layoutOffsetsCheckOnly). Their offsets are
// compared with check's types.Layout only.

package corpus

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// zzlayoutModule is std/zzlayout/put.mk, added to the std copy only.
const zzlayoutModule = `// Added by internal/corpus (diff_offsets_test.go) to a copy of std: the
// layout probes write inside a value's bytes. Not part of the repository's std.
import std.raw

// Put writes the low n bytes of x, least significant first, at byte off of
// the value v refers to.
export Put[T](v: mut T, off: int, n: int, x: u64) {
    unsafe "a layout probe writes inside its own value's bytes" {
        p := raw.Offset(raw.PtrCast[T, u8](raw.OfMut(v)), off)
        var b := x
        for i in 0..n {
            raw.Write(raw.Offset(p, i), Trunc[u8](b))
            b = b >> 8
        }
    }
}

export SliceAddr[T](s: []T): u64 {
    return Cast[u64](raw.Addr(raw.Data(s)))
}

export StrAddr(s: str): u64 {
    return Cast[u64](raw.Addr(raw.StrData(s)))
}
`

// offsetProbe checks one golden offset of a row natively: Pre runs first
// (it may declare locals), then var v: <the row's type> = Init, m: u64 =
// Mark, the low Width bytes of m are written at the golden offset, and Holds
// (over v and m) must be true.
type offsetProbe struct {
	Pre   string
	Init  string
	Width int
	Mark  string
	Holds string
}

// layoutProbeDecls are the helpers the probes' Holds call.
const layoutProbeDecls = `
DiffShapeTag(s: Shape): int {
    match s {
        .Circle {
            return 0
        }
        .Rect {
            return 1
        }
        .Empty {
            return 2
        }
    }
}

DiffShapeR(s: Shape): f64 {
    match s {
        .Circle(r) {
            return r
        }
        .Rect {
            return -1.0
        }
        .Empty {
            return -1.0
        }
    }
}

DiffTokTag(t: Tok): int {
    match t {
        .Num {
            return 0
        }
        .Ch {
            return 1
        }
    }
}

DiffTokN(t: Tok): u16 {
    match t {
        .Num(n) {
            return n
        }
        .Ch {
            return 0
        }
    }
}
`

// mixedProbes are the probes of Mixed, PackedMixed and Packed2Mixed, which
// have the same fields.
func mixedProbes(typ string) []offsetProbe {
	init := typ + "(a: 0, b: 0, c: 0, d: 0, e: 0)"
	return []offsetProbe{
		{Init: init, Width: 1, Mark: "0xA1", Holds: "v.a == 0xA1"},
		{Init: init, Width: 2, Mark: "0xB2B3", Holds: "v.b == 0xB2B3"},
		{Init: init, Width: 1, Mark: "0xC4", Holds: "v.c == 0xC4"},
		{Init: init, Width: 4, Mark: "0xD5D6D7D8", Holds: "v.d == 0xD5D6D7D8"},
		{Init: init, Width: 1, Mark: "0xE9", Holds: "v.e == 0xE9"},
	}
}

// sliceProbes are the probes of a slice row: its pointer, moved by 8 (an
// address compared, never dereferenced), and its length.
func sliceProbes(pre, init string) []offsetProbe {
	return []offsetProbe{
		{Pre: pre, Init: init, Width: 8, Mark: "zzlayout.SliceAddr(v) + 8", Holds: "zzlayout.SliceAddr(v) == m"},
		{Pre: pre, Init: init, Width: 8, Mark: "1", Holds: "v.Len() == 1"},
	}
}

// layoutOffsetProbes are the probes of every golden row with offsets, in
// the order of the row's offsets (goldenOffsets' convention: a record's
// fields by name). A row with offsets that has no probes and is not in
// layoutOffsetsCheckOnly fails the test.
var layoutOffsetProbes = map[string][]offsetProbe{
	"SliceU8":  sliceProbes("var arr: [16]u8 = [0; 16]", "arr[0..]"),
	"SliceI64": sliceProbes("var arr: [2]i64 = [0; 2]", "arr[0..]"),
	"Str": {
		{Init: `"0123456789abcdef"`, Width: 8, Mark: "zzlayout.StrAddr(v) + 1", Holds: "zzlayout.StrAddr(v) == m"},
		{Init: `"0123456789abcdef"`, Width: 8, Mark: "3", Holds: "v.Len() == 3"},
	},
	"ArrayU16": {
		{Init: "[0; 3]", Width: 2, Mark: "0xA1B1", Holds: "v[0] == 0xA1B1"},
		{Init: "[0; 3]", Width: 2, Mark: "0xA2B2", Holds: "v[1] == 0xA2B2"},
		{Init: "[0; 3]", Width: 2, Mark: "0xA3B3", Holds: "v[2] == 0xA3B3"},
	},
	"ArrayPair": {
		{Init: "[Pair(a: 0, b: 0); 2]", Width: 1, Mark: "0x5C", Holds: "v[0].a == 0x5C"},
		{Init: "[Pair(a: 0, b: 0); 2]", Width: 1, Mark: "0x5D", Holds: "v[1].a == 0x5D"},
	},
	"Pair": {
		{Init: "Pair(a: 0, b: 0)", Width: 1, Mark: "0x5A", Holds: "v.a == 0x5A"},
		{Init: "Pair(a: 0, b: 0)", Width: 8, Mark: "0x1122334455667788", Holds: "v.b == 0x1122334455667788"},
	},
	"Mixed":        mixedProbes("Mixed"),
	"PackedMixed":  mixedProbes("PackedMixed"),
	"Packed2Mixed": mixedProbes("Packed2Mixed"),
	"Nested": {
		{Init: "Nested(p: Pair(a: 0, b: 0), x: 0)", Width: 1, Mark: "0x5A", Holds: "v.p.a == 0x5A"},
		{Init: "Nested(p: Pair(a: 0, b: 0), x: 0)", Width: 1, Mark: "0x7E", Holds: "v.x == 0x7E"},
	},
	"Tail": {
		{Init: "Tail(x: 0, b: false)", Width: 4, Mark: "0x12345678", Holds: "v.x == 0x12345678"},
		{Init: "Tail(x: 0, b: false)", Width: 1, Mark: "1", Holds: "v.b"},
	},
	"Record": {
		{Init: "(a: 0, b: 0)", Width: 8, Mark: "0x0102030405060708", Holds: "v.a == 0x0102030405060708"},
		{Init: "(a: 0, b: 0)", Width: 1, Mark: "0x5B", Holds: "v.b == 0x5B"},
	},
	"Shape": {
		{Init: "Shape.Circle(r: 0.0)", Width: 1, Mark: "2", Holds: "DiffShapeTag(v) == 2"},
		{Init: "Shape.Circle(r: 0.0)", Width: 8, Mark: "0x4004000000000000", Holds: "DiffShapeR(v) == 2.5"},
	},
	"Tok": {
		{Init: "Tok.Num(n: 0)", Width: 1, Mark: "1", Holds: "DiffTokTag(v) == 1"},
		{Init: "Tok.Num(n: 0)", Width: 2, Mark: "0xBEEF", Holds: "DiffTokN(v) == 0xBEEF"},
	},
	"OptI32": {
		{Init: "some(0)", Width: 1, Mark: "0", Holds: "v.IsNone()"},
		{Init: "some(0)", Width: 4, Mark: "0x7A6B5C4D", Holds: "v.IsSome() && v.Must() == 0x7A6B5C4D"},
	},
	"OptU8": {
		{Init: "some(0)", Width: 1, Mark: "0", Holds: "v.IsNone()"},
		{Init: "some(0)", Width: 1, Mark: "0x9C", Holds: "v.IsSome() && v.Must() == 0x9C"},
	},
	"OptPair": {
		{Init: "some(Pair(a: 0, b: 0))", Width: 1, Mark: "0", Holds: "v.IsNone()"},
		{Init: "some(Pair(a: 0, b: 0))", Width: 1, Mark: "0x5E", Holds: "v.IsSome() && v.Must().a == 0x5E"},
	},
	"Align64Pair": {
		{Init: "Align64Pair(a: 0, b: 0)", Width: 1, Mark: "0x5A", Holds: "v.a == 0x5A"},
		{Init: "Align64Pair(a: 0, b: 0)", Width: 8, Mark: "0x1122334455667788", Holds: "v.b == 0x1122334455667788"},
	},
	"HoldsAligned": {
		{Init: "HoldsAligned(x: Align16(a: 0), y: 0)", Width: 1, Mark: "0x5A", Holds: "v.x.a == 0x5A"},
		{Init: "HoldsAligned(x: Align16(a: 0), y: 0)", Width: 1, Mark: "0x7E", Holds: "v.y == 0x7E"},
	},
	"HoldsVec": {
		{Init: "HoldsVec(a: 0, v: simd.Vec[f32, 4].Splat(0.0))", Width: 1, Mark: "0x5A", Holds: "v.a == 0x5A"},
		{Init: "HoldsVec(a: 0, v: simd.Vec[f32, 4].Splat(0.0))", Width: 4, Mark: "0x3FC00000", Holds: "v.v.Lane(0) == 1.5"},
	},
}

// layoutOffsetsCheckOnly are the golden rows whose offsets no native probe
// reaches, with the reason; TestLayoutOffsetsChecked still compares
// them with check's types.Layout. An E!T value cannot be held in a local or
// passed as an argument (E0802: its error must be handled where the call
// is), so there is no value whose bytes a probe could write.
var layoutOffsetsCheckOnly = map[string]string{
	"BadI64": "an E!T is not a value a local can hold (E0802)",
	"BadU8":  "an E!T is not a value a local can hold (E0802)",
	"BigU8":  "an E!T is not a value a local can hold (E0802)",
}

// offsetRef names one probe: the golden row and the index of its offset.
type offsetRef struct {
	Row   LayoutRow
	Index int
}

// layoutOffsetSource is layout.mk with std.zzlayout imported and one probe
// function per golden offset appended, and a Main that exits with the
// number of the first probe that fails (1-based, refs[n-1]), or 0. types
// maps each row to the type of its Layout function's SizeOf. An offset whose
// part does not fit inside the row's size is returned in bad, unprobed. The
// rows of layoutOffsetsCheckOnly are left out.
func layoutOffsetSource(src []byte, rows []LayoutRow, types map[string]string) (gen []byte, refs []offsetRef, bad []string, err error) {
	var b bytes.Buffer
	b.WriteString("import std.zzlayout\n\n")
	b.Write(src)
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n// Appended by internal/corpus (TestDiffLayout's native offsets): one probe per golden offset.\n")
	b.WriteString(layoutProbeDecls)
	var calls []string
	for _, r := range rows {
		if _, checkOnly := layoutOffsetsCheckOnly[r.Name]; len(r.Offsets) == 0 || checkOnly {
			continue
		}
		probes, ok := layoutOffsetProbes[r.Name]
		if !ok || len(probes) != len(r.Offsets) {
			return nil, nil, nil, fmt.Errorf("golden row %s has %d offsets and %d native probes (layoutOffsetProbes)", r.Name, len(r.Offsets), len(probes))
		}
		typ, ok := types[r.Name]
		if !ok {
			return nil, nil, nil, fmt.Errorf("golden row %s has no Layout%s in layout.mk", r.Name, r.Name)
		}
		for i, p := range probes {
			off := r.Offsets[i]
			if off+p.Width > r.Size {
				bad = append(bad, fmt.Sprintf("%d: %s: offset %d of part %d plus its width %d passes the size %d", r.Line, r.Name, off, i, p.Width, r.Size))
				continue
			}
			refs = append(refs, offsetRef{Row: r, Index: i})
			fn := fmt.Sprintf("DiffOffset%s%d", r.Name, i)
			fmt.Fprintf(&b, "\n%s(): bool {\n", fn)
			if p.Pre != "" {
				fmt.Fprintf(&b, "    %s\n", p.Pre)
			}
			fmt.Fprintf(&b, "    var v: %s = %s\n    m: u64 = %s\n    zzlayout.Put(mut v, %d, %d, m)\n    return %s\n}\n", typ, p.Init, p.Mark, off, p.Width, p.Holds)
			calls = append(calls, fn)
		}
	}
	if len(calls) > 120 {
		return nil, nil, nil, fmt.Errorf("%d probes: the native check numbers them by exit status, at most 120", len(calls))
	}
	b.WriteString("\nexport Main(sys: &System): int {\n")
	for i, fn := range calls {
		fmt.Fprintf(&b, "    if !%s() {\n        return %d\n    }\n", fn, i+1)
	}
	b.WriteString("    return 0\n}\n")
	return b.Bytes(), refs, bad, nil
}

// layoutProbeStd is a copy of the repository's std with std/zzlayout added
// and listed in pkg.mk's unsafe_modules (E1102).
func layoutProbeStd(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "std", "pkg.mk"))
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	if !strings.Contains(m, "unsafe_modules: [") {
		t.Fatal("std/pkg.mk has no unsafe_modules list to add zzlayout to")
	}
	m = strings.Replace(m, "unsafe_modules: [", `unsafe_modules: ["zzlayout", `, 1)
	return digitsStd(t, map[string]string{"pkg.mk": m, "zzlayout/put.mk": zzlayoutModule})
}

// layoutNativeOffsets builds the offset program for target and prof over
// std and runs it: it must exit 0. The same program with Mixed's last
// offset planted wrong (12 to 13, a padding byte) must exit with that
// probe's number, so a probe that checks nothing cannot pass.
func layoutNativeOffsets(t *testing.T, r *Runner, dir string, src []byte, rows []LayoutRow, types map[string]string, target, prof, golden string) {
	t.Helper()
	planted := slices.Clone(rows)
	i := slices.IndexFunc(planted, func(r LayoutRow) bool { return r.Name == "Mixed" })
	if i < 0 || len(planted[i].Offsets) == 0 || planted[i].Offsets[len(planted[i].Offsets)-1] != 12 {
		t.Fatalf("%s: no row Mixed whose last offset is 12 to plant", golden)
	}
	planted[i].Offsets = slices.Clone(planted[i].Offsets)
	planted[i].Offsets[len(planted[i].Offsets)-1] = 13
	var checkOnly []string
	for _, name := range slices.Sorted(maps.Keys(layoutOffsetsCheckOnly)) {
		k := slices.IndexFunc(rows, func(r LayoutRow) bool { return r.Name == name })
		if k < 0 || len(rows[k].Offsets) == 0 {
			t.Errorf("layoutOffsetsCheckOnly names %s, which is no golden row with offsets: remove it", name)
			continue
		}
		checkOnly = append(checkOnly, fmt.Sprintf("%s (%d)", name, len(rows[k].Offsets)))
	}
	t.Logf("offsets not probed natively, compared with check's types.Layout only: %s", strings.Join(checkOnly, ", "))
	for _, c := range []struct {
		name    string
		rows    []LayoutRow
		planted bool
	}{{"offsets.mk", rows, false}, {"offsets_planted.mk", planted, true}} {
		gen, refs, bad, err := layoutOffsetSource(src, c.rows, types)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bad {
			t.Errorf("%s:%s", golden, b)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name), gen, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Build(c.name, target, prof); err != nil {
			t.Fatal(err)
		}
		needWSLRun(t, target, true)
		got, err := r.Run(ProgramCase{Program: c.name, Timeout: DefaultCaseTimeout}, target, prof)
		want := 0
		if c.planted {
			want = slices.IndexFunc(refs, func(f offsetRef) bool {
				return f.Row.Name == "Mixed" && f.Index == len(f.Row.Offsets)-1
			}) + 1
		}
		switch {
		case ErrNoRunner(err):
			skipOrFatal(t, gateRun(), "%v", err)
		case err != nil:
			t.Fatal(err)
		case got.TimedOut:
			t.Fatalf("%s: timed out after %v", c.name, DefaultCaseTimeout)
		case got.Exit == want && len(got.Stdout) == 0 && len(got.Stderr) == 0:
			if !c.planted {
				t.Logf("%d offsets: every native probe holds", len(refs))
			}
		case c.planted:
			t.Errorf("with Mixed's last offset planted as 13: exit %d, stderr %q; want exit %d (that probe)", got.Exit, got.Stderr, want)
		case got.Exit >= 1 && got.Exit <= len(refs):
			f := refs[got.Exit-1]
			t.Errorf("%s:%d: natively, part %d of %s is not at offset %d", golden, f.Row.Line, f.Index, f.Row.Name, f.Row.Offsets[f.Index])
		default:
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 0", c.name, got.Exit, got.Stdout, got.Stderr)
		}
	}
}

// layoutOffsetSource's numbering, planting and fit check, without a build.
func TestLayoutOffsetSource(t *testing.T) {
	rows := []LayoutRow{
		{Name: "I8", Size: 1, Align: 1, Line: 1},
		{Name: "Pair", Size: 16, Align: 8, Offsets: []int{0, 8}, Line: 2},
	}
	types := map[string]string{"I8": "i8", "Pair": "Pair"}
	gen, refs, bad, err := layoutOffsetSource([]byte("struct Pair {\n    a: u8\n    b: i64\n}"), rows, types)
	if err != nil || len(bad) > 0 {
		t.Fatalf("%v %v", err, bad)
	}
	if len(refs) != 2 || refs[1].Row.Name != "Pair" || refs[1].Index != 1 {
		t.Errorf("refs %+v", refs)
	}
	for _, want := range []string{
		"import std.zzlayout\n\nstruct Pair {",
		"DiffOffsetPair1(): bool {\n    var v: Pair = Pair(a: 0, b: 0)\n    m: u64 = 0x1122334455667788\n    zzlayout.Put(mut v, 8, 8, m)\n    return v.b == 0x1122334455667788\n}\n",
		"    if !DiffOffsetPair1() {\n        return 2\n    }\n    return 0\n}\n",
	} {
		if !strings.Contains(string(gen), want) {
			t.Errorf("generated source lacks %q:\n%s", want, gen)
		}
	}
	// Pair's 8 planted as 9: an 8-byte part at 9 passes the size 16, so it
	// is reported unprobed, and the native run cannot miss it.
	rows[1].Offsets = []int{0, 9}
	if _, refs, bad, err := layoutOffsetSource(nil, rows, types); err != nil || len(bad) != 1 || len(refs) != 1 || !strings.Contains(bad[0], "Pair: offset 9") {
		t.Errorf("Pair at 9: refs %d, bad %q, %v", len(refs), bad, err)
	}
	rows = append(rows, LayoutRow{Name: "Nope", Size: 8, Align: 8, Offsets: []int{0, 4}})
	types["Nope"] = "Nope"
	if _, _, _, err := layoutOffsetSource(nil, rows, types); err == nil || !strings.Contains(err.Error(), "native probes") {
		t.Errorf("a row with offsets and no probes: %v, want a native probes error", err)
	}
}
