// Why: a wrong C ABI lowering corrupts every foreign call silently, so each
// target's lowering of a fixed suite is pinned.
// What: the suite goldens per target (TestABIWin64Suite and its siblings),
// TestExtensionRows, TestABIClassifyMemoized, TestABISlotInvariants,
// TestABINotConcrete and BenchmarkABILower.
// How: lowers every row of testdata/suite.txt and compares with
// testdata/<target>.golden, whose header names the clang cross-check it
// agrees with.

package abi

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"mk/internal/types"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// lowerRow lowers one suite row.
func lowerRow(w *world, target types.Target, r row) Sig {
	return Lower(w.tt, w.layout, target, r.params, r.fixed, r.variadic, r.result)
}

// golden reads testdata/<target>.golden: header lines start with '#', and
// every other line is "<row>: <declaration>".
type golden struct {
	header []string
	lines  map[string]string
	order  []string
}

func readFileLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), nil
}

func readGolden(path string) (*golden, error) {
	lines, err := readFileLines(path)
	if err != nil {
		return nil, err
	}
	g := &golden{lines: map[string]string{}}
	for _, ln := range lines {
		switch {
		case ln == "", strings.HasPrefix(ln, "="):
		case strings.HasPrefix(ln, "#"):
			g.header = append(g.header, ln)
		default:
			name, decl, ok := strings.Cut(ln, ": ")
			if !ok {
				continue
			}
			g.lines[name] = decl
			g.order = append(g.order, name)
		}
	}
	return g, nil
}

// goldenHeader is the header of a golden file: what it is and the clang
// cross-check it agrees with, taken from the oracle's header.
func goldenHeader(t *testing.T, target types.Target) []string {
	h := []string{
		"# Lowered declarations of testdata/suite.txt for " + target.Name + ".",
		"# Rewritten only by go test ./internal/abi -update; read the diff before keeping it.",
	}
	if o, err := readGolden(oraclePath(target)); err == nil {
		for _, ln := range o.header {
			if strings.HasPrefix(ln, "# clang:") || strings.HasPrefix(ln, "# command:") {
				h = append(h, "# cross-checked against "+strings.TrimPrefix(ln, "# "))
			}
		}
	} else {
		t.Logf("no clang oracle for %s: %v", target.Name, err)
	}
	return h
}

func suiteGolden(t *testing.T, target types.Target) {
	w := loadSuite(t)
	path := "testdata/" + target.Name + ".golden"
	var got []string
	for _, r := range w.rows {
		got = append(got, r.name+": "+printSig(w, target, r, lowerRow(w, target, r)))
	}
	if *update {
		text := strings.Join(append(goldenHeader(t, target), got...), "\n") + "\n"
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	g, err := readGolden(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/abi -update)", err)
	}
	if len(g.order) != len(w.rows) {
		t.Errorf("%s has %d rows, the suite %d", path, len(g.order), len(w.rows))
	}
	for i, r := range w.rows {
		want, ok := g.lines[r.name]
		_, line, _ := strings.Cut(got[i], ": ")
		switch {
		case !ok:
			t.Errorf("%s: row %s missing", path, r.name)
		case want != line:
			t.Errorf("%s: %s (suite.txt:%d)\n got %s\nwant %s", path, r.name, r.line, line, want)
		}
	}
}

func TestABIWin64Suite(t *testing.T)   { suiteGolden(t, types.X86_64Windows) }
func TestABISysVSuite(t *testing.T)    { suiteGolden(t, types.X86_64Linux) }
func TestABIAAPCS64Suite(t *testing.T) { suiteGolden(t, types.Aarch64Linux) }
func TestABIDarwinSuite(t *testing.T)  { suiteGolden(t, types.Aarch64Macos) }

// TestExtensionRows pins the zero and sign extension of small integers on
// parameters and results, independent of the goldens.
func TestExtensionRows(t *testing.T) {
	w := newWorld()
	cases := []struct {
		ty                       types.TypeID
		win, sysv, linux, darwin Ext
	}{
		{types.Bool, ZeroExt, ZeroExt, ExtNone, ZeroExt},
		{types.I8, ExtNone, SignExt, ExtNone, SignExt},
		{types.U8, ExtNone, ZeroExt, ExtNone, ZeroExt},
		{types.I16, ExtNone, SignExt, ExtNone, SignExt},
		{types.U16, ExtNone, ZeroExt, ExtNone, ZeroExt},
		{types.I32, ExtNone, ExtNone, ExtNone, ExtNone},
		{types.Char, ExtNone, ExtNone, ExtNone, ExtNone},
		{types.F32, ExtNone, ExtNone, ExtNone, ExtNone},
	}
	for _, c := range cases {
		for _, tc := range []struct {
			target types.Target
			want   Ext
		}{{types.X86_64Windows, c.win}, {types.X86_64Linux, c.sysv}, {types.Aarch64Linux, c.linux}, {types.Aarch64Macos, c.darwin}} {
			s := Lower(w.tt, w.layout, tc.target, []types.TypeID{c.ty}, 1, false, c.ty)
			if s.Params[0].Ext != tc.want || s.Ret.Ext != tc.want {
				t.Errorf("%s on %s: param %v result %v, want %v", w.tt.TypeString(c.ty, nil), tc.target.Name,
					s.Params[0].Ext, s.Ret.Ext, tc.want)
			}
		}
	}
	// The Win64 example row: f(a: i8, b: u16) is void @f(i8 noundef, i16 noundef).
	r := row{name: "f", params: []types.TypeID{types.I8, types.U16}, fixed: 2, result: types.Void}
	if got := printSig(w, types.X86_64Windows, r, lowerRow(w, types.X86_64Windows, r)); got != "void @f(i8 noundef, i16 noundef)" {
		t.Errorf("Win64 f(i8, u16) = %s", got)
	}
}

// TestABIClassifyMemoized checks what lets callers memoize Classify per
// worker: it is pure and deterministic, from many goroutines at once, and it
// equals the Lower of a one-parameter signature.
func TestABIClassifyMemoized(t *testing.T) {
	w := loadSuite(t)
	var ts []types.TypeID
	for _, r := range w.rows {
		ts = append(ts, r.params...)
	}
	for _, target := range abiTargets() {
		want := make([]types.AbiClass, len(ts))
		for i, ty := range ts {
			want[i] = Classify(w.tt, w.layout, target, ty)
			if l := Lower(w.tt, w.layout, target, []types.TypeID{ty}, 1, false, types.Void); toClass(l.Params[0]) != want[i] {
				t.Errorf("%s %s: Classify %+v, Lower %+v", target.Name, w.tt.TypeString(ty, nil), want[i], l.Params[0])
			}
		}
		var wg sync.WaitGroup
		errs := make([]int, 8)
		for g := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i, ty := range ts {
					if Classify(w.tt, w.layout, target, ty) != want[i] {
						errs[g]++
					}
				}
			}()
		}
		wg.Wait()
		for g, n := range errs {
			if n != 0 {
				t.Errorf("%s: goroutine %d saw %d different classes", target.Name, g, n)
			}
		}
	}
}

func BenchmarkABILower(b *testing.B) {
	w := loadSuite(b)
	b.ReportAllocs()
	for b.Loop() {
		for _, r := range w.rows {
			Lower(w.tt, w.layout, types.X86_64Linux, r.params, r.fixed, r.variadic, r.result)
		}
	}
	b.ReportMetric(float64(len(w.rows)), "sigs/op")
}

// TestABISlotInvariants checks every lowered suite slot on every target
// against the invariants Slot's doc comment states.
func TestABISlotInvariants(t *testing.T) {
	w := loadSuite(t)
	for _, target := range abiTargets() {
		for _, r := range w.rows {
			sig := lowerRow(w, target, r)
			check := func(what string, ty types.TypeID, s Slot, result bool) {
				size, _ := w.sa(ty)
				fail := func(msg string) {
					t.Errorf("%s %s %s (%s): %s: %+v", target.Name, r.name, what, w.tt.TypeString(ty, nil), msg, s)
				}
				if (s.Kind == Ignore) != (size == 0) {
					fail("Ignore exactly when zero-sized")
				}
				switch s.Kind {
				case Direct:
					// SysV passes one part of at most 8 bytes per eightbyte holding
					// data; elsewhere the parts cover the whole value.
					covered := uint64(0)
					for _, p := range s.Parts {
						covered += uint64(p.Bytes)
					}
					switch {
					case len(s.Parts) == 0 || len(s.Parts) > 4:
						fail("Direct has 1 to 4 parts")
					case isSysV(target):
						for _, p := range s.Parts {
							if p.Bytes > 8 || uint64(len(s.Parts)) > (size+7)/8 {
								fail("a SysV part is at most one eightbyte")
							}
						}
					case covered < size:
						fail("Direct parts must cover the value")
					}
				case Indirect:
					if s.Byval && (result || !isSysV(target) || s.Align < 8) {
						fail("byval only for SysV parameters, aligned to at least 8")
					}
					if len(s.Parts) != 0 || s.Size != size {
						fail("Indirect carries no parts and the value's size")
					}
				}
				if s.Ext != ExtNone && (s.Kind != Direct || len(s.Parts) != 1 || s.Parts[0].Bytes >= 4) {
					fail("extension only on a Direct scalar below 32 bits")
				}
			}
			check("result", r.result, sig.Ret, true)
			for i, p := range r.params {
				check("param "+strconv.Itoa(i), p, sig.Params[i], false)
			}
		}
	}
}

// TestABINotConcrete: a generic parameter reaching abi is a caller's bug,
// raised as a panic that source.Guard turns into an internal error.
func TestABINotConcrete(t *testing.T) {
	w := newWorld()
	defer func() {
		r := recover()
		err, ok := r.(error)
		if !ok || !strings.HasPrefix(err.Error(), "abi: ") {
			t.Fatalf("recovered %v, want an abi error", r)
		}
	}()
	Classify(w.tt, w.layout, types.X86_64Linux, w.tt.Param(0))
}
