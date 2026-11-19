// Why: std.core is layered (0: no allocator, 1: no Io) for freestanding
// builds; a call that goes up a layer breaks them.
// What: TestLayerZeroFiles and TestLayerZeroFilesSeesPlants.
// How: walks check's resolved bodies (Body.Uses) of std.core, std.math and
// std.atomic, so methods, interface-bound calls and AllocTable and IoTable
// values count, not only names. Package std's TestLayerZeroTextScan
// and TestLayerOneTextScan are the text-scan companions.

package stdtest

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/check"
	"mk/internal/source"
	"mk/internal/types"
)

// layerMods are the modules the layer rule covers: std.core by file prefix,
// std.math and std.atomic as layer 0 (both import std.raw only), except
// std.math's io_rng.mk, and std.target and std.simd, which baseMods holds
// since std.core imports them (fileLayer).
var layerMods = slices.Concat(baseMods, []modDir{{"std.atomic", "atomic"}, {"std.math", "math"}})

// layerRaw is the "layer" of std.raw: intrinsics, callable from every layer.
const layerRaw = -2

// fileLayer is the layer of a std file path ("std/core/x.mk"): in std/core,
// core_* 0, mem_*, coll_* and text_* 1, io_* and sync_* 2; every file of
// std.math, std.atomic, std.simd and std.target 0, except std.math's io_rng.mk
// and std.atomic's io_wait.mk, 2; std.raw layerRaw; -1 for the rest (tests_*
// files and other modules).
func fileLayer(p string) int {
	dir, b := path.Base(path.Dir(p)), path.Base(p)
	switch {
	case dir == "raw":
		return layerRaw
	case strings.HasPrefix(b, "tests_"):
		return -1
	case dir == "math" && b == "io_rng.mk":
		// Rng.FromIo calls Io.Random, a layer-2 method, so it has a file of
		// its own that a freestanding build without Io leaves out, and the
		// rest of std.math stays layer 0 (the file's io_ prefix is
		// std/core's layer-2 prefix).
		return 2
	case dir == "atomic" && b == "io_wait.mk":
		// Atomic[T].Wait takes an Io and parks through its table, so it
		// is layer 2 in a file of its own, as io_rng.mk is; the rest of
		// std.atomic, Spin included, stays layer 0.
		return 2
	case dir == "math", dir == "atomic", dir == "simd", dir == "target":
		// std.simd and std.target are layer 0 too (doc/std/README.md), below
		// std.core, which imports them.
		return 0
	case dir != "core":
		return -1
	case strings.HasPrefix(b, "core_"):
		return 0
	case strings.HasPrefix(b, "mem_"), strings.HasPrefix(b, "coll_"), strings.HasPrefix(b, "text_"):
		return 1
	case strings.HasPrefix(b, "io_"), strings.HasPrefix(b, "sync_"):
		return 2
	}
	return -1
}

// layerFinding is one reach a layer forbids.
type layerFinding struct {
	at     string // file:line of the use
	caller string // the unit's item key
	reach  string // what it reaches
	why    string
}

func (f layerFinding) String() string {
	return fmt.Sprintf("%s: %s (layer %s) reaches %s", f.at, f.caller, f.why, f.reach)
}

// layerReport is what the walk saw: the findings, and counts of the calls
// it allowed by kind (logged, so a reading that allows too much shows).
type layerReport struct {
	findings []layerFinding
	allowed  map[string]int
	units    int
	noItem   []string // calls check recorded with no callee item
	diags    map[string]int
}

// layerWalk checks every unit of layerMods' modules in root (a std
// directory) and returns what the layer rule forbids.
func layerWalk(t *testing.T) *layerReport {
	t.Helper()
	cs := checkStdMods(t, layerMods)
	key := func(name string) types.ItemID {
		id, ok := cs.items.ByKey(types.ItemKey("std.core", name))
		if !ok {
			t.Fatalf("no std.core.%s", name)
		}
		return id
	}
	allocTable, ioTable := key("AllocTable"), key("IoTable")
	writerTable, readerTable := key("WriterTable"), key("ReaderTable")
	rep := &layerReport{allowed: map[string]int{}, diags: map[string]int{}}
	seen := map[string]bool{}
	pathOf := func(f source.FileID) string { return cs.sm.File(f).Path }
	at := func(s source.Span) string {
		lc := cs.sm.LineCol(source.Pos{File: s.File, Off: s.Start})
		return fmt.Sprintf("%s:%d", pathOf(s.File), lc.Line)
	}
	for _, m := range cs.checked {
		for _, unit := range m.Units() {
			it := cs.items.Get(unit)
			layer := fileLayer(pathOf(it.Span.File))
			if layer < 0 {
				continue
			}
			var b *check.Body
			if ie := source.Guard("check "+it.Key.String(), it.Span, func() { b = cs.c.Check(m, unit) }); ie != nil {
				t.Errorf("%s: check ICE: %v", it.Key, ie)
				continue
			}
			rep.units++
			for _, d := range b.Diags {
				rep.diags[d.Code.String()]++
			}
			use := func(n ast.Node) (check.Use, bool) {
				k := int(n.ID()) - int(b.Base)
				if k < 0 || k >= len(b.Uses) {
					return check.Use{}, false
				}
				return b.Uses[k], true
			}
			add := func(n ast.Node, reach, why string) {
				f := layerFinding{at: at(n.Span()), caller: it.Key.String(), reach: reach, why: why}
				if !seen[f.String()] {
					seen[f.String()] = true
					rep.findings = append(rep.findings, f)
				}
			}
			ast.Inspect(b.Node, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FieldExpr:
					// An AllocTable or IoTable entry, called or only read
					// (a read is how a call through a local would start).
					u, ok := use(n)
					if !ok || u.Kind != check.UseField || u.Item == types.NoItem {
						return true
					}
					switch {
					case u.Item == ioTable && layer < 2:
						add(n, "IoTable."+cs.in.Str(n.Name.Name), fmt.Sprintf("%d: no IoTable entry below layer 2", layer))
					case u.Item == allocTable && layer < 1:
						add(n, "AllocTable."+cs.in.Str(n.Name.Name), "0: no AllocTable entry in layer 0")
					}
				case *ast.CallExpr:
					u, ok := use(n)
					if !ok {
						return true
					}
					switch u.Kind {
					case check.UseItem, check.UseMethod, check.UseIfaceMethod:
						if u.Item == types.NoItem {
							// A call of a const function parameter (`less`
							// in core_sort.mk): check records UseItem with
							// no item and the name as the parameter's local.
							if fu, ok := use(n.Fun); ok && fu.Kind == check.UseLocal && b.Sig != nil && int(fu.Local) >= 1 && int(fu.Local) <= len(b.Sig.Params) {
								rep.allowed["a const function parameter"]++
								return true
							}
							rep.noItem = append(rep.noItem, fmt.Sprintf("%s in %s (use kind %d, callee %T)", at(n.Span()), it.Key, u.Kind, n.Fun))
							return true
						}
						callee := cs.items.Get(u.Item)
						cl := fileLayer(pathOf(callee.Span.File))
						switch {
						case cl == layerRaw:
							rep.allowed["intrinsic"]++
						case cl < 0:
							add(n, callee.Key.String(), fmt.Sprintf("%d: declared in %s, which has no layer", layer, pathOf(callee.Span.File)))
						case cl > layer:
							add(n, callee.Key.String(), fmt.Sprintf("%d: declared in layer %d (%s)", layer, cl, pathOf(callee.Span.File)))
						default:
							rep.allowed[fmt.Sprintf("layer %d", cl)]++
						}
					case check.UseFnValueCall:
						what := "a function value"
						if fe, ok := n.Fun.(*ast.FieldExpr); ok {
							if fu, ok := use(fe); ok && fu.Kind == check.UseField {
								switch fu.Item {
								case writerTable, readerTable:
									what = "a WriterTable or ReaderTable entry"
								case allocTable, ioTable:
									what = "an AllocTable or IoTable entry" // the FieldExpr case judges it
								}
							}
						}
						rep.allowed[what]++
					case check.UseBuiltin:
						rep.allowed["builtin"]++
					}
				}
				return true
			})
		}
	}
	return rep
}

// TestLayerZeroFiles: no body of a layer-0 file (core_*, std.math
// but its io_rng.mk, std.atomic) calls a function or method declared in a layer-1 or layer-2
// file or reads an AllocTable or IoTable entry; no body of a layer-1 file
// (mem_*, coll_*, text_*) calls a layer-2 function or reads an IoTable
// entry; no layered body calls into a tests_* file. Intrinsics (std.raw,
// raw.Fail among them), builtins, interface methods through a bound (the
// interface is declared in core_iface.mk; the impl is the caller's type's)
// and caller-supplied function values (WriterTable and ReaderTable entries,
// a const less) are allowed, as the layer rule says.
func TestLayerZeroFiles(t *testing.T) {
	rep := layerWalk(t)
	for _, f := range rep.findings {
		t.Errorf("%s (4.1 rule 5)", f)
	}
	if rep.units < 100 {
		t.Errorf("only %d layered units checked; the loader lost std's bodies", rep.units)
	}
	var kinds []string
	for k, n := range rep.allowed {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
	}
	slices.Sort(kinds)
	t.Logf("%d layered units; allowed calls: %s", rep.units, strings.Join(kinds, ", "))
	t.Logf("body diagnostics (no evaluator here, so E0901 is expected): %v", rep.diags)
	for _, s := range rep.noItem {
		t.Errorf("call with no callee item, which this test cannot judge: %s", s)
	}
}

// plantStd copies mods of the live std into a temporary directory, rewrites
// each named file with its edit, and points stdRoot at the copy for the
// rest of the test.
func plantStd(t *testing.T, mods []modDir, edits map[string]func(string) string) {
	t.Helper()
	root := stdRoot(t)
	dir := t.TempDir()
	for _, m := range mods {
		if err := os.CopyFS(filepath.Join(dir, m.dir), os.DirFS(filepath.Join(root, m.dir))); err != nil {
			t.Fatal(err)
		}
	}
	for file, edit := range edits {
		p := filepath.Join(dir, filepath.FromSlash(file))
		old, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(old), "\r\n", "\n")
		planted := edit(text)
		if planted == text {
			t.Fatalf("%s: the plant changed nothing", file)
		}
		if err := os.WriteFile(p, []byte(planted), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MK_STDTEST_STD", dir)
}

// appendText is a plantStd edit that appends add.
func appendText(add string) func(string) string {
	return func(s string) string { return s + add }
}

// TestLayerZeroFilesSeesPlants: the walk fires on misplacements
// planted into a std copy, one for each reach the layer rule
// forbids: a Sleep body in a core_* file (an IoTable entry, IoTab and the
// layer-2 Io.Sleep), FmtArena called from core_fmt.mk (layer 1 from layer
// 0), an AllocTable entry and Arena.Put from layer 0, and a layer-1 body
// that calls Io.Yield and IoNow (layer 2) and reads an IoTable entry; and it
// does not fire on the planted bodies' allowed calls (raw.Fail,
// Duration.Nanos, str.Len).
func TestLayerZeroFilesSeesPlants(t *testing.T) {
	plantStd(t, layerMods, map[string]func(string) string{
		"core/core_iodecl.mk": appendText(`
PlantedSleep(io: Io, d: Duration): Canceled!void {
    f := IoTab(io).sleep
    if f(io, d.Nanos()).err != SysErr.None {
        raw.Fail(8, "planted")
    }
    try io.Sleep(d)
}
`),
		"core/core_fmt.mk": appendText(`
PlantedFmtArena(a: &Arena, h: &Heap, ps: []FmtPiece, args: []FmtArg): OutOfMemory!int {
    t := try FmtArena(a, ps, args)
    _ = t.Len()
    f := h.own.table.alloc
    _ = f
    _ = try a.Put(1)
    return 0
}
`),
		"core/mem_box.mk": appendText(`
PlantedLayerOne(io: Io): Canceled!int {
    try io.Yield()
    r := IoNow(io)
    f := IoTab(io).now
    _ = f
    return int(r.value)
}
`),
	})
	rep := layerWalk(t)
	got := map[string]bool{}
	for _, f := range rep.findings {
		if strings.Contains(f.caller, "Planted") {
			got[f.caller+" "+f.reach] = true
		} else {
			t.Errorf("finding outside the plants: %s", f)
		}
	}
	for _, want := range []string{
		"std.core.PlantedSleep IoTable.sleep",
		"std.core.PlantedSleep std.core.IoTab",
		"std.core.PlantedSleep std.core.Io:Sleep",
		"std.core.PlantedFmtArena std.core.FmtArena",
		"std.core.PlantedFmtArena AllocTable.alloc",
		"std.core.PlantedFmtArena std.core.Arena:Put",
		"std.core.PlantedLayerOne std.core.Io:Yield",
		"std.core.PlantedLayerOne std.core.IoNow",
		"std.core.PlantedLayerOne std.core.IoTab",
		"std.core.PlantedLayerOne IoTable.now",
	} {
		if !got[want] {
			t.Errorf("planted reach %q not found; findings: %v", want, slices.Sorted(func(yield func(string) bool) {
				for k := range got {
					if !yield(k) {
						return
					}
				}
			}))
		}
	}
	if len(got) > 10 {
		t.Errorf("more findings than plants: %v", got)
	}
}
