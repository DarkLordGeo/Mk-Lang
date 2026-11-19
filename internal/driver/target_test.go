// Why: programs read the build target at compile time through the Target
// const, so every build, corpus unit and std must see the right value.
// What: TestTargetConstPerBuild, TestTargetVectorBits,
// TestTargetConstInCorpusUnit and TestNoTargetItemWithoutTargetInfo.
// How: the driver synthesizes the const item std.core.Target of type
// TargetInfo, names it in KnownItems.Target and gives it the build target's
// value; the tests check consts that read it against a stand-in std.

package driver

import (
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

// targetStdFiles is the stand-in std with the reference's TargetInfo, Arch
// and Os in place of the stand-in's member-less ones.
func targetStdFiles(t *testing.T) map[string]string {
	t.Helper()
	files := fakeStdFiles()
	core := files["core/core.mk"]
	for old, repl := range map[string]string{
		"export struct TargetInfo {}\n": "export struct TargetInfo {\n    export arch: Arch\n    export os: Os\n    export pointer_bytes: int\n}\n",
		"export enum Arch { A }\n":      "export enum Arch {\n    X64\n    Arm64\n    Riscv64\n}\n",
		"export enum Os { A }\n":        "export enum Os {\n    Linux\n    Windows\n    Macos\n    Freestanding\n}\n",
	} {
		if !strings.Contains(core, old) {
			t.Fatalf("stand-in std.core has no %q", old)
		}
		core = strings.Replace(core, old, repl, 1)
	}
	files["core/core.mk"] = core
	return files
}

// pageSizeSrc is the reference's PageSize example plus consts
// that read Target at compile time.
const pageSizeSrc = "PageSize(): int {\n    if Target.arch == Arch.Arm64 {\n        return 16384\n    }\n    return 4096\n}\n\n" +
	"export const Page: int = PageSize()\n\n" +
	"export const Ptr: int = Target.pointer_bytes\n\n" +
	"OnWindows(): bool {\n    return Target.os == Os.Windows\n}\n\n" +
	"export const Win: bool = OnWindows()\n"

func TestTargetConstPerBuild(t *testing.T) {
	stdDir := writeTree(t, targetStdFiles(t))
	root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": pageSizeSrc}))
	for _, tc := range []struct {
		target    types.Target
		page, win string
	}{
		{types.X86_64Windows, "4096", "true"},
		{types.X86_64Linux, "4096", "false"},
		{types.Aarch64Linux, "16384", "false"},
	} {
		t.Run(tc.target.Name, func(t *testing.T) {
			s, err := NewSession(Config{Root: root, StdDir: stdDir, Target: tc.target})
			if err != nil {
				t.Fatal(err)
			}
			r := s.Check()
			clean(t, r)
			if r.Reached != StageFlow {
				t.Errorf("reached %v, want flow", r.Reached)
			}
			k := s.w.sem5.known
			if k.Target == types.NoItem {
				t.Fatal("KnownItems.Target is NoItem")
			}
			it := s.w.items.Get(k.Target)
			if it.Key.String() != "std.core.Target" || it.Kind != types.KindConst {
				t.Errorf("Target item %s, kind %v", it.Key, it.Kind)
			}
			if d, ok := it.Decl.(*types.ConstDecl); !ok || d.Type != s.w.tab.Named(k.TargetInfo, nil) || !d.HasValue {
				t.Errorf("Target decl %#v, want a const of TargetInfo with its value", it.Decl)
			} else if v := s.w.tab.Consts().Get(d.Value); len(v.Bytes) != 16 || v.Bytes[8] != 8 {
				// arch and os are one-byte tags at 0 and 1, pointer_bytes
				// the int at 8 (architecture 4.3).
				t.Errorf("Target value bytes %v", v.Bytes)
			}
			for key, want := range map[string]string{"a.Page": tc.page, "a.Ptr": "8", "a.Win": tc.win} {
				v, ok := constValue(t, s, key)
				if !ok {
					t.Errorf("%s has no value", key)
					continue
				}
				if got := s.w.tab.ConstString(v); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}

// TestTargetVectorBits: with the vector_bits field in TargetInfo,
// buildTarget stores the target's fixed VectorBits, so a const reading
// Target.vector_bits is 128 on every current target, never zero.
func TestTargetVectorBits(t *testing.T) {
	files := targetStdFiles(t)
	files["core/core.mk"] = strings.Replace(files["core/core.mk"], "    export pointer_bytes: int\n}\n",
		"    export pointer_bytes: int\n    export vector_bits: int\n}\n", 1)
	stdDir := writeTree(t, files)
	root := writeTree(t, pkgFiles(map[string]string{"a/a.mk": "export const Bits: int = Target.vector_bits\n\n" +
		"export const Lanes32: int = Target.vector_bits / 32\n"}))
	for _, tg := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux} {
		t.Run(tg.Name, func(t *testing.T) {
			s, err := NewSession(Config{Root: root, StdDir: stdDir, Target: tg})
			if err != nil {
				t.Fatal(err)
			}
			clean(t, s.Check())
			it := s.w.items.Get(s.w.sem5.known.Target)
			d, ok := it.Decl.(*types.ConstDecl)
			if !ok || !d.HasValue {
				t.Fatalf("Target decl %#v has no value", it.Decl)
			}
			// arch, os at 0 and 1, pointer_bytes at 8, vector_bits at 16.
			if v := s.w.tab.Consts().Get(d.Value); len(v.Bytes) != 24 || v.Bytes[8] != 8 || v.Bytes[16] != 128 || v.Bytes[17] != 0 {
				t.Errorf("Target value bytes %v", v.Bytes)
			}
			for key, want := range map[string]string{"a.Bits": strconv.Itoa(int(tg.VectorBits)), "a.Lanes32": "4"} {
				v, ok := constValue(t, s, key)
				if !ok {
					t.Errorf("%s has no value", key)
					continue
				}
				if got := s.w.tab.ConstString(v); got != want {
					t.Errorf("%s = %s, want %s", key, got, want)
				}
			}
		})
	}
}

func TestTargetConstInCorpusUnit(t *testing.T) {
	// The corpus session synthesizes the item once, with std, and a unit's
	// body that reads Target (the reference's PageSize) lowers.
	c, err := NewCorpusSession(Config{StdDir: writeTree(t, targetStdFiles(t))})
	if err != nil {
		t.Fatal(err)
	}
	out := c.CheckUnit(UnitInput{Name: "doc/reference.md:660",
		Files: []UnitFile{{"block.mk", "PageSize(): int {\n    if Target.arch == Arch.Arm64 {\n        return 16384\n    }\n    return 4096\n}\n"}}})
	if len(out.Report.Diags) > 0 {
		t.Errorf("want no diagnostics, got:\n%s", out.Report.Text(out.Sources))
	}
}

func TestNoTargetItemWithoutTargetInfo(t *testing.T) {
	// A std whose prelude has no TargetInfo leaves KnownItems.Target NoItem
	// (the inert case of types.KnownItems): no item is synthesized.
	files := fakeStdFiles()
	files["core/core.mk"] = strings.Replace(files["core/core.mk"], "export struct TargetInfo {}\n", "", 1)
	s, err := NewSession(Config{Root: writeTree(t, pkgFiles(map[string]string{"a/a.mk": "export F(): int {\n    return 1\n}\n"})),
		StdDir: writeTree(t, files)})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, s.Check())
	if _, ok := s.w.items.ByKey(types.KeyFromString("std.core.Target")); ok {
		t.Error("std.core.Target synthesized with no TargetInfo")
	}
}
