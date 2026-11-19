// Why: the x86_64-freestanding target changes what a program may import,
// where it starts, which instructions it may use and how it links; the
// driver applies each rule.
// What: tests of the import set, entry and runtime roots,
// TestGeneralRegsOnlyAtCheck, TestHostedBinFields, tests of the link line and
// C flags, and TestCPUFloor.
// How: checks and builds small packages for hosted and freestanding targets
// and compares diagnostics, roots and the link and C command lines.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// freeCheck checks the single file text for x86_64-freestanding over the
// tree's std.
func freeCheck(t *testing.T, text string) Result {
	t.Helper()
	dir := writeTree(t, map[string]string{"main.mk": text})
	return check(t, Config{Root: filepath.Join(dir, "main.mk"), Target: types.X86_64Freestanding})
}

const freeEntry = `
export extern MkEntry() {
    for {
    }
}
`

// TestFreestandingLayers: on the freestanding target std.fs, std.json
// and std.rt are E0202 reason freestanding; layer 0 and 1, std.free,
// derive and dyn are accepted.
func TestFreestandingLayers(t *testing.T) {
	t.Parallel()
	for _, mod := range []string{"std.fs", "std.json", "std.rt", "std.task", "std.time"} {
		t.Run("reject/"+mod, func(t *testing.T) {
			t.Parallel()
			short := mod[strings.LastIndex(mod, ".")+1:]
			r := freeCheck(t, "import "+mod+"\n\nUse(): str {\n    _ = "+short+".Nothing\n    return \"\"\n}\n"+freeEntry)
			var hit *diag.Diagnostic
			for _, e := range r.Report.Diags {
				if e.Diag.Code == diag.E0202 {
					hit = e.Diag
				}
			}
			if hit == nil || fact(hit, "reason") != "freestanding" || fact(hit, "module") != mod {
				t.Fatalf("want E0202 reason freestanding for %s, got:\n%s", mod, r.Report.Text(r.Sources))
			}
		})
	}
	for _, mod := range []string{"std.mmio", "std.hash", "std.mem", "std.coll", "std.text", "std.free", "std.atomic", "std.math"} {
		t.Run("accept/"+mod, func(t *testing.T) {
			t.Parallel()
			short := mod[strings.LastIndex(mod, ".")+1:]
			r := freeCheck(t, "import "+mod+"\n\nUse() {\n    _ = "+short+".NoSuchItem\n}\n"+freeEntry)
			for _, e := range r.Report.Diags {
				if e.Diag.Code == diag.E0202 {
					t.Fatalf("%s refused on freestanding:\n%s", mod, r.Report.Text(r.Sources))
				}
			}
		})
	}
}

// TestFreeCheckClean: a freestanding program of layer 0 with no Main
// checks clean (the entry is MkEntry), and std.free is no module on
// a hosted target.
func TestFreeCheckClean(t *testing.T) {
	t.Parallel()
	clean(t, freeCheck(t, "Add(a: int, b: int): int {\n    return a + b\n}\n"+freeEntry))
	dir := writeTree(t, map[string]string{"main.mk": "import std.free\n\nexport Main(sys: &System) {\n    _ = free.Heap()\n}\n"})
	r := check(t, Config{Root: filepath.Join(dir, "main.mk"), Target: types.X86_64Windows})
	if !slices.Contains(codes(r), "E0202") {
		t.Errorf("hosted import of std.free: want E0202, got:\n%s", r.Report.Text(r.Sources))
	}
}

// TestNoRtRoots: the freestanding runtime roots name std.free's panic
// hooks and never std.rt or std.task; the float remainder roots only join a
// bin that is not general-regs-only.
func TestNoRtRoots(t *testing.T) {
	t.Parallel()
	for _, gro := range []bool{true, false} {
		keys := rootsFor(types.X86_64Freestanding, gro)
		var mods []string
		for _, k := range keys {
			s := k.String()
			if strings.HasPrefix(s, "std.rt") || strings.HasPrefix(s, "std.task") {
				t.Errorf("gro %t: freestanding root %s", gro, s)
			}
			mods = append(mods, s)
		}
		hasRem := slices.ContainsFunc(mods, func(s string) bool { return strings.Contains(s, "Rem64") })
		if hasRem == gro {
			t.Errorf("gro %t: Rem64 rooted %t", gro, hasRem)
		}
		if !slices.ContainsFunc(mods, func(s string) bool { return strings.Contains(s, "std.free") && strings.Contains(s, "PanicAt") }) {
			t.Errorf("gro %t: no std.free PanicAt root in %v", gro, mods)
		}
	}
	if got := rootsFor(types.X86_64Windows, false); !slices.Equal(got, runtimeRoots) {
		t.Errorf("hosted roots changed: %v", got)
	}
	if !stdFormed(types.X86_64Freestanding, "std.free") || stdFormed(types.X86_64Freestanding, "std.rt") ||
		stdFormed(types.X86_64Linux, "std.free") || !stdFormed(types.X86_64Linux, "std.rt") {
		t.Error("stdFormed disagrees with the freestanding std rule")
	}
}

// TestGeneralRegsOnlyAtCheck: mk check of a freestanding bin reports
// E1108 for a float its entry reaches, with the bin fact, and nothing for a
// float function no root reaches (mk check runs the same walk as mk build).
func TestGeneralRegsOnlyAtCheck(t *testing.T) {
	t.Parallel()
	r := freeCheck(t, `Half(x: f64): f64 {
    return x / 2.0
}

Unused(x: f64): f64 {
    return x * 3.0
}

export extern MkEntry() {
    _ = Half(1.0)
    for {
    }
}
`)
	var got []*diag.Diagnostic
	for _, e := range r.Report.Diags {
		got = append(got, e.Diag)
	}
	if len(got) == 0 {
		t.Fatal("want E1108, got a clean report")
	}
	for _, d := range got {
		if d.Code != diag.E1108 {
			t.Errorf("want only E1108, got %s: %s", d.Code, d.Message)
			continue
		}
		if fact(d, "bin") != "main" || strings.Contains(fact(d, "instance"), "Unused") {
			t.Errorf("E1108 facts: bin %q instance %q", fact(d, "bin"), fact(d, "instance"))
		}
	}
}

// TestHostedBinFields: the allowed values of pkg.Bin's cpu ("",
// "general-regs-only", "baseline") and link_script, judged per target
// (validateBins).
func TestHostedBinFields(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, map[string]string{"k.ld": "SECTIONS {}\n", "src/m.mk": ""})
	tree := diskTree(dir, "", os.ReadFile)
	cases := []struct {
		target      types.Target
		cpu, script string
		field       string // "" when accepted
	}{
		{types.X86_64Windows, "", "", ""},
		{types.X86_64Windows, "baseline", "", ""},
		{types.X86_64Linux, "general-regs-only", "", "cpu"},
		{types.X86_64Linux, "x86-64-v3", "", "cpu"},
		{types.X86_64Windows, "", "k.ld", "link_script"},
		{types.X86_64Freestanding, "", "k.ld", ""},
		{types.X86_64Freestanding, "general-regs-only", "", ""},
		{types.X86_64Freestanding, "baseline", "", ""},
		{types.X86_64Freestanding, "native", "", "cpu"},
		{types.X86_64Freestanding, "", "missing.ld", "link_script"},
		{types.X86_64Freestanding, "", "../k.ld", "link_script"},
	}
	for _, c := range cases {
		m := &Manifest{Bins: []Bin{{Name: "k", Root: "src", CPU: c.cpu, LinkScript: c.script}}, binSrc: []int{0}}
		ds := validateBins(m, tree, c.target)
		switch {
		case c.field == "" && len(ds) > 0:
			t.Errorf("%s cpu %q script %q: want accepted, got %s", c.target.Name, c.cpu, c.script, ds[0].Message)
		case c.field != "" && (len(ds) != 1 || ds[0].Code != diag.E0202 || fact(ds[0], "field") != c.field):
			t.Errorf("%s cpu %q script %q: want one E0202 at %s, got %d", c.target.Name, c.cpu, c.script, c.field, len(ds))
		}
	}
	if !generalRegsOnly(types.X86_64Freestanding, "") || generalRegsOnly(types.X86_64Freestanding, "baseline") ||
		generalRegsOnly(types.X86_64Windows, "") {
		t.Error("generalRegsOnly disagrees with the general-registers rule")
	}
}

// TestFreeLinkArgs: the freestanding link line (toolchain/free.go):
// a static ELF with no PIE and no RELRO, entered at MkEntry, the linker
// script before the objects; a script on a hosted link is refused.
func TestFreeLinkArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out, obj, script := filepath.Join(dir, "k"), filepath.Join(dir, "a.o"), filepath.Join(dir, "k.ld")
	if toolchain.FormatOf(toolchain.FreeTriple) != toolchain.ELF {
		t.Fatalf("FormatOf(%s) = %s", toolchain.FreeTriple, toolchain.FormatOf(toolchain.FreeTriple))
	}
	args, err := toolchain.LinkArgs(toolchain.LinkJob{Format: toolchain.ELF, Free: true, Script: script, Output: out, Objects: []string{obj}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-static", "-nostdlib", "--no-pie", "-z", "norelro", "--build-id=none", "-z", "noexecstack",
		"-e", "MkEntry", "-o", out, "-T", script, obj}
	if !slices.Equal(args, want) {
		t.Errorf("args\n got %q\nwant %q", args, want)
	}
	if _, err := toolchain.LinkArgs(toolchain.LinkJob{Format: toolchain.ELF, Script: script, Output: out, Objects: []string{obj}}); err == nil {
		t.Error("a hosted ELF link took a linker script")
	}
	if _, err := toolchain.LinkArgs(toolchain.LinkJob{Format: toolchain.ELF, Free: true, Script: "k.ld", Output: out, Objects: []string{obj}}); err == nil {
		t.Error("a relative linker script was accepted")
	}
}

// TestCTargetFlags: a bin's C jobs get the build's CPU as -march on
// x86_64, and on x86_64-freestanding -mno-red-zone and, when the bin is
// general-regs-only, -mgeneral-regs-only, before the manifest's flags.
func TestCTargetFlags(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		target types.Target
		cpu    string
		gro    bool
		want   string
	}{
		{types.X86_64Windows, "x86-64-v2", false, "-march=x86-64-v2"},
		{types.X86_64Linux, "znver5", false, "-march=znver5"},
		{types.X86_64Freestanding, "x86-64", true, "-march=x86-64 -mno-red-zone -mgeneral-regs-only"},
		{types.X86_64Freestanding, "x86-64", false, "-march=x86-64 -mno-red-zone"},
	} {
		if got := strings.Join(cTargetFlags(c.target, c.cpu, c.gro), " "); got != c.want {
			t.Errorf("%s cpu %s gro %t: %q, want %q", c.target.Name, c.cpu, c.gro, got, c.want)
		}
	}
}

// TestCPUFloor: on a hosted x86_64 target a --cpu without x86-64-v2's
// features (core2 lacks sse4.1, sse4.2 and popcnt) is one E0001 with the
// fact flag --cpu, exit 2, and nehalem, the first such CPU with them,
// builds; on x86_64-freestanding core2 builds.
func TestCPUFloor(t *testing.T) {
	t.Parallel()
	tools := needTools(t)
	build := func(t *testing.T, root string, target types.Target, cpu string) Result {
		t.Helper()
		s, err := NewSession(Config{Root: root, Target: target, CPU: cpu, Tools: tools, NoCache: true})
		if err != nil {
			t.Fatal(err)
		}
		r, _, err := s.Build(filepath.Join(t.TempDir(), "p"+exeSuffix(target)))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("hosted", func(t *testing.T) {
		t.Parallel()
		src := helloFile(t)
		r := build(t, src, HostTarget(), "core2")
		if st := ExitStatus(r.Report); st != 2 || len(r.Report.Diags) != 1 {
			t.Fatalf("--cpu=core2: exit %d, %d diagnostics, want 2 and one E0001\n%s", st, len(r.Report.Diags), r.Report.Text(r.Sources))
		}
		d := r.Report.Diags[0].Diag
		if d.Code != diag.E0001 || !hasFact(d, "flag", "--cpu") || !strings.Contains(d.Message, "sse4.2") || !strings.Contains(d.Message, "popcnt") {
			t.Errorf("--cpu=core2: %s %q, want E0001 naming sse4.2 and popcnt with flag --cpu", d.Code, d.Message)
		}
		if r := build(t, src, HostTarget(), "nehalem"); len(r.Report.Diags) > 0 {
			t.Errorf("--cpu=nehalem:\n%s", r.Report.Text(r.Sources))
		}
	})
	t.Run("freestanding", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "main.mk")
		if err := os.WriteFile(file, []byte(freeLayer0), 0o644); err != nil {
			t.Fatal(err)
		}
		if r := build(t, file, types.X86_64Freestanding, "core2"); len(r.Report.Diags) > 0 {
			t.Errorf("freestanding --cpu=core2:\n%s", r.Report.Text(r.Sources))
		}
	})
}
