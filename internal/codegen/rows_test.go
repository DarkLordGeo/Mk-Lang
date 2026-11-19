// Why: FuncTable rows must not keep every function alive, or /OPT:REF and
// --gc-sections drop nothing.
// What: TestRowsFollowFunctions: each row follows its function,
// natively, with native and ThinLTO packs.
// How: rowGroups reads the linked rows; ThinLTO runs with its cache
// directory as toolchain passes it.

package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// rev03Unit is funcTableUnit with Twice not Local and a third function
// Dead(n) = n *% 5 (line 11) in pack 0 that nothing calls.
func rev03Unit(e *testEnv) *Unit {
	u := funcTableUnit(e)
	u.Funcs[0].Local = false
	id := e.fn("Dead", []types.TypeID{types.Int}, types.Int, 0)
	e.items.Get(id).Span = source.Span{File: 1, Start: 10 * 100, End: 10*100 + 4}
	f := e.body(id)
	f.assign(place(0), bin(mir.MulWrap, mir.Copy(place(1)), mir.Imm(types.Int, 5)), f.at(12, 5))
	f.ret(f.at(12, 5))
	u.Packs[0] = append(u.Packs[0], len(u.Funcs))
	u.Funcs = append(u.Funcs, Func{Symbol: "mk.main.Dead", Key: "main.Dead", Item: id, Body: f.b, Pack: 0})
	return u
}

// rowGroups splits funcTableHarness's output into its per-row groups of
// four lines (line, column, file length, f(7)), sorted, and the last line.
func rowGroups(out string) ([]string, string) {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines)%4 != 1 {
		return nil, out
	}
	var gs []string
	for i := 0; i+4 <= len(lines)-1; i += 4 {
		gs = append(gs, strings.Join(lines[i:i+4], " "))
	}
	sort.Strings(gs)
	return gs, lines[len(lines)-1]
}

// TestRowsFollowFunctions: the harness calls Twice and Thrice and
// scans FuncTable. Linked without discarding (dev), the table has rows for
// Twice, Thrice and the unreached Dead; linked with /opt:ref or
// --gc-sections, from native objects and from ThinLTO bitcode, Dead and its
// row are gone and the reached functions keep theirs with their sites. The
// ThinLTO link fills its cache directory, and a second link from the cache
// gives the same executable.
func TestRowsFollowFunctions(t *testing.T) {
	sections := "-ffunction-sections -fdata-sections"
	for _, target := range runTargets {
		for _, mode := range []string{"dev", "release", "thinlto"} {
			t.Run(target.Name+"/"+mode, func(t *testing.T) {
				if _, why := hostRunner(target); why != "" {
					skipRunner(t, why)
				}
				e := newTestEnv()
				u := rev03Unit(e)
				prof, popt := types.Dev, "-O0 "+sections
				switch mode {
				case "release":
					prof, popt = types.Release, "-O2 "+sections
				case "thinlto":
					prof, popt = types.Release, "-O2 -flto=thin "+sections
				}
				cfg := Config{Target: target, Profile: prof}
				dir := t.TempDir()
				var objs []string
				for p := range u.Packs {
					var b strings.Builder
					if _, err := EmitPack(u, p, e, cfg, &b); err != nil {
						t.Fatal(err)
					}
					objs = append(objs, clangC(t, target, dir, "pack"+string(rune('0'+p)), b.String(), popt))
				}
				var sb strings.Builder
				if err := EmitSupport(u, e, cfg, &sb); err != nil {
					t.Fatal(err)
				}
				osIR, kernel := harnessOS(target)
				decls := "declare i64 @mk.main.Twice(i64)\ndeclare i64 @mk.main.Thrice(i64)\n"
				pre := "  %keep1 = call i64 @mk.main.Twice(i64 1)\n  %keep2 = call i64 @mk.main.Thrice(i64 1)\n"
				objs = append(objs, clangC(t, target, dir, "support", sb.String(), "-O2 "+sections),
					clangC(t, target, dir, "harness", osIR+harnessRT+funcTableHarness(target, decls, pre), "-O0 "+sections))
				lo := linkOpts{kernel: kernel, release: mode != "dev", lto: mode == "thinlto", noref: mode == "dev"}
				if lo.lto {
					lo.cache = filepath.Join(dir, "thinlto")
					if err := os.MkdirAll(lo.cache, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				exe := linkExe(t, target, dir, objs, lo)
				out, code := runExe(t, target, exe, "")
				want := []string{"3 1 9 14", "7 1 9 21"}
				if mode == "dev" {
					want = append(want, "11 1 9 35")
				}
				sort.Strings(want)
				gs, last := rowGroups(out)
				if code != 0 || strings.Join(gs, "\n") != strings.Join(want, "\n") || last != "1" {
					t.Fatalf("exit %d, rows\n%s\nwant\n%s\n(output %q)", code, strings.Join(gs, "\n"), strings.Join(want, "\n"), out)
				}
				if !lo.lto {
					return
				}
				ents, err := os.ReadDir(lo.cache)
				if err != nil || len(ents) == 0 {
					t.Fatalf("the ThinLTO cache %s is empty (%v)", lo.cache, err)
				}
				first, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				exe2 := linkExe(t, target, dir, objs, lo)
				second, err := os.ReadFile(exe2)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(first, second) {
					t.Error("a link from the ThinLTO cache gives another executable")
				}
				ents2, _ := os.ReadDir(lo.cache)
				if len(ents2) != len(ents) {
					t.Errorf("the second link changed the cache: %d entries, then %d", len(ents), len(ents2))
				}
			})
		}
	}
}
