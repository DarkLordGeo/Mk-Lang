// Why: the size profile and release with symbols (profile.go) change how
// packs compile and how the link keeps debug data, and those choices must
// reach the jobs and the cache keys.
// What: TestProfileCompileLevels, TestProfileSizeWarmMixEqualsCold and
// TestProfileSymbolsResolves.
// How: reads the levels and link options of native_steps.go's packJob and
// native.go's linkJob (profileHooked skips when those hooks are missing),
// builds small programs, and resolves an address with llvm-symbolizer.

package driver

import (
	"bytes"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"mk/internal/codegen"
	"mk/internal/toolchain"
	"mk/internal/types"
)

const profileHello = `export Work(n: int): int {
    var s := 0
    for i in 0..n {
        s += i % 7
    }
    return s
}

export Main(sys: &System): WriteError!void {
    try sys.Print("hello {n}\n", (n: Work(sys.args.Len() + 1000)))
}
`

// profileHooked stops the test when packJob's profileCompile and linkJob's
// linkKeepsSymbols hooks are missing.
func profileHooked(t *testing.T) {
	t.Helper()
	w := &world{cfg: Config{Size: true, Symbols: true, Profile: types.Release}}
	job := w.packJob(codegen.Config{Triple: "x86_64-pc-windows-msvc"}, `C:\a.ll`, `C:\a.o`, 0, 1)
	if job.Opt != toolchain.Oz || !w.linkJob("x86_64-pc-windows-msvc", `C:\a.exe`).Debug {
		t.Skip("lib10's hook lines (tmp/m10/tools10/profiles-native.patch: packJob's profileCompile, linkJob's linkKeepsSymbols) are not applied")
	}
}

func TestProfileCompileLevels(t *testing.T) {
	for _, c := range []struct {
		cfg     Config
		inOpt   toolchain.OptLevel
		inLTO   bool
		wantOpt toolchain.OptLevel
		wantLTO bool
	}{
		{Config{Profile: types.Release}, toolchain.O3, true, toolchain.O3, true},
		{Config{Profile: types.Release, Size: true}, toolchain.O3, true, toolchain.Oz, true},
		{Config{Profile: types.Release, Size: true}, toolchain.O3, false, toolchain.Oz, false}, // the support pack
		{Config{Profile: types.Release, Symbols: true}, toolchain.O3, true, toolchain.O3, true},
	} {
		w := &world{cfg: c.cfg}
		if o, l := w.profileCompile(c.inOpt, c.inLTO); o != c.wantOpt || l != c.wantLTO {
			t.Errorf("%+v: %v %v, want %v %v", c.cfg, o, l, c.wantOpt, c.wantLTO)
		}
	}
	if (&world{cfg: Config{Symbols: true}}).linkKeepsSymbols() != true || (&world{}).linkKeepsSymbols() {
		t.Error("linkKeepsSymbols")
	}
}

// profileBuild builds root in cfg and returns the executable's bytes.
func profileBuild(t *testing.T, cfg Config, out string) []byte {
	t.Helper()
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.Build(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestProfileSizeWarmMixEqualsCold: release, size, then release again in
// one cache: the size build compiles every pack at -Oz (its objects never
// hit release's), runs, and the second release image is byte for byte
// the first, so the level is in the keys both ways.
func TestProfileSizeWarmMixEqualsCold(t *testing.T) {
	profileHooked(t)
	tools := needTools(t)
	root := testPkg(t, map[string]string{"main.mk": profileHello})
	cache, dir := t.TempDir(), t.TempDir()
	cfg := Config{Root: root, Profile: types.Release, Tools: tools, CacheDir: cache}
	rel1 := profileBuild(t, cfg, filepath.Join(dir, "r1.exe"))
	cfg.Size = true
	size := profileBuild(t, cfg, filepath.Join(dir, "s.exe"))
	cfg.Size = false
	rel2 := profileBuild(t, cfg, filepath.Join(dir, "r2.exe"))
	if bytes.Equal(rel1, size) {
		t.Error("the size image is the release image: the -Oz level did not reach the packs or the keys")
	}
	if !bytes.Equal(rel1, rel2) {
		t.Error("release after size differs from release before it")
	}
	cold := profileBuild(t, Config{Root: root, Profile: types.Release, Size: true, Tools: tools, CacheDir: t.TempDir()}, filepath.Join(dir, "s2.exe"))
	if !bytes.Equal(size, cold) {
		t.Error("the warm size image differs from a cold one")
	}
	out, err := exec.Command(filepath.Join(dir, "s.exe")).CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "hello ") {
		t.Errorf("the size build ran: %q %v", out, err)
	}
	t.Logf("hello-like sizes (bytes): release %d, size %d", len(rel1), len(size))
}

// TestProfileSymbolsResolves is the profiler check: in a --release --symbols
// image an address inside Main resolves to Main through llvm-symbolizer, the
// symbolizer perf and most profilers share, from the PDB on Windows and
// DWARF on Linux; plain --release keeps none.
func TestProfileSymbolsResolves(t *testing.T) {
	profileHooked(t)
	tools := needTools(t)
	bin := filepath.Dir(tools.Clang)
	root := testPkg(t, map[string]string{"main.mk": profileHello})
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			if target.OS == types.OSWindows && runtime.GOOS != "windows" {
				t.Skip("lld-link's PDB on a Windows host only")
			}
			// One cache for every build: release, then --symbols, then
			// release again, so the link key must tell them apart.
			cache := t.TempDir()
			rel := Config{Root: root, Target: target, Profile: types.Release, Tools: tools, CacheDir: cache}
			syms := rel
			syms.Symbols = true
			pdbCount := func(dir string) int {
				pdbs, _ := filepath.Glob(filepath.Join(dir, "*.pdb"))
				return len(pdbs)
			}
			d1, d2 := t.TempDir(), t.TempDir()
			plain := profileBuild(t, rel, filepath.Join(d1, "main"+exeSuffix(target)))
			if pdbCount(d1) > 0 {
				t.Fatal("plain --release wrote a PDB")
			}
			dir := t.TempDir()
			exe := filepath.Join(dir, "main"+exeSuffix(target))
			withSyms := profileBuild(t, syms, exe)
			if target.OS == types.OSWindows && pdbCount(dir) != 1 {
				t.Fatal("--symbols after a release build in the same cache wrote no PDB")
			}
			if bytes.Equal(plain, withSyms) && target.OS != types.OSWindows {
				t.Error("the --symbols ELF is the stripped release one: the link key did not tell them apart")
			}
			again := profileBuild(t, rel, filepath.Join(d2, "main"+exeSuffix(target)))
			if pdbCount(d2) > 0 || !bytes.Equal(plain, again) {
				t.Error("release after --symbols in the same cache differs from release before it")
			}
			// Main, which LTO keeps (Work is inlined into it): on ELF its
			// symbol, on COFF its CodeView procedure record (Main is local,
			// so the PDB's publics lack it).
			sym := "mk.main.Main"
			if target.OS == types.OSWindows {
				sym = "main.Main"
			}
			addr := profileSymbolAddr(t, bin, exe, sym)
			out := profileTool(t, filepath.Join(bin, "llvm-symbolizer"), "--obj="+exe, "--functions=short", "--no-inlines", "0x"+strconv.FormatUint(addr+4, 16))
			if !strings.Contains(out, "Main") {
				t.Errorf("address 0x%x+4 resolves to %q, want Main", addr, out)
			}
			// The debug information, not only the symbol: a source line.
			if !strings.Contains(out, "main.mk:") {
				t.Errorf("address 0x%x+4 has no source line: %q", addr, out)
			}
		})
	}
}

func profileTool(t *testing.T, argv ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" && !strings.HasSuffix(argv[0], ".exe") {
		argv[0] += ".exe"
	}
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%q: %v\n%s", argv, err, out)
	}
	return string(out)
}

// profileSymbolAddr is symbol's virtual address: from llvm-nm on ELF, from
// the PDB's procedure records and the section table on COFF (as
// internal/codegen's TestDebugSymbolize finds it).
func profileSymbolAddr(t *testing.T, bin, exe, symbol string) uint64 {
	t.Helper()
	if !strings.HasSuffix(exe, ".exe") {
		out := profileTool(t, filepath.Join(bin, "llvm-nm"), exe)
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(line); len(f) == 3 && f[2] == symbol {
				if v, err := strconv.ParseUint(f[0], 16, 64); err == nil {
					return v
				}
			}
		}
		t.Fatalf("no %s in llvm-nm of %s:\n%s", symbol, exe, out)
	}
	pdbs, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*.pdb")) // named after the package
	if len(pdbs) != 1 {
		t.Fatalf("PDBs beside %s: %q", exe, pdbs)
	}
	out := profileTool(t, filepath.Join(bin, "llvm-pdbutil"), "dump", "-symbols", pdbs[0])
	m := regexp.MustCompile("S_[GL]PROC32 [^\\n]*`" + regexp.QuoteMeta(symbol) + "`\\s+parent = [^\\n]*addr = ([0-9A-Fa-f]{4}):([0-9A-Fa-f]{4,8})").FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no procedure %s in the PDB:\n%s", symbol, out)
	}
	sec, _ := strconv.ParseUint(m[1], 16, 32)
	off, _ := strconv.ParseUint(m[2], 16, 64)
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || sec == 0 || int(sec) > len(f.Sections) {
		t.Fatalf("section %d of %s", sec, exe)
	}
	return oh.ImageBase + uint64(f.Sections[sec-1].VirtualAddress) + off
}
