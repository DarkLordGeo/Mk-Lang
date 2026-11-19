// Why: full debug info is only right if a real debugger can use it.
// What: TestDebuggerSmokeWindows and TestDebuggerSmokeLinux (stop in a
// function and print a local), TestDebugSymbolize (an address to a name),
// TestDebugExeDeterministic (two links in two directories are identical).
// How: a pack is emitted with Config.Debug, compiled and linked through
// toolchain (LinkJob.Debug: a PDB on COFF, DWARF kept on ELF) and run under
// cdb or lldb; a missing debugger is a skip with the reason.

package codegen

import (
	"bytes"
	"context"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// debugHarness is the program's mk.std.task.Start: print Work(14) (Work
// computes x = 42, y = 43 and returns Probe(43) = 1849) and exit 0.
const debugHarness = `
declare i64 @mk.main.Work(i64)

define void @"mk.std.task.Start"(ptr %sp) noreturn nounwind {
  %r = call i64 @mk.main.Work(i64 14)
  call void @h_num(i64 %r)
  call void @h_write(ptr @s.nl, i64 1)
  call void @h_exit(i32 0)
  unreachable
}
`

// debugExe builds the debugWork program for target in dir: the pack with
// Config.Debug at prof (dev at -O0; release as ThinLTO bitcode at -O3,
// linked with LTO), the support pack and the harness, linked through
// toolchain.Link with Debug. It returns the executable's path.
func debugExe(t *testing.T, target types.Target, prof types.Profile, dir string) string {
	t.Helper()
	return debugExeWith(t, target, prof, dir, false)
}

// debugExeWith is debugExe; with nativeLTO a release pack is compiled to a
// native object at -O3 (no bitcode) and still linked with LTO.
func debugExeWith(t *testing.T, target types.Target, prof types.Profile, dir string, nativeLTO bool) string {
	t.Helper()
	bin := needLLVM(t)
	tools, err := toolchain.Discover(toolchain.Options{LLVMDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	e := newTestEnv()
	pack := emitDebug(t, e, unitOf(e, debugWork(e)), target, prof)
	osIR, kernel := harnessOS(target)
	rel := prof == types.Release
	var objs []string
	for _, m := range []struct {
		name, src string
		opt       toolchain.OptLevel
		lto       bool
	}{
		{"pack", pack, toolchain.O0, false},
		{"support", supportLL(t, target), toolchain.O3, false},
		{"harness", osIR + harnessRT + debugHarness, toolchain.O0, false},
	} {
		if m.name == "pack" && rel {
			m.opt, m.lto = toolchain.O3, !nativeLTO
		}
		ll := filepath.Join(dir, m.name+".ll")
		if err := os.WriteFile(ll, []byte(m.src), 0o644); err != nil {
			t.Fatal(err)
		}
		obj := filepath.Join(dir, m.name+".o")
		if err := tools.Compile(ctx, toolchain.CompileJob{Triple: target.LLVMTriple, Input: ll, Output: obj,
			Opt: m.opt, LTO: m.lto, CPU: "x86-64-v2"}); err != nil {
			t.Fatal(err)
		}
		objs = append(objs, obj)
	}
	job := toolchain.LinkJob{Format: toolchain.ELF, Output: filepath.Join(dir, "t"), Objects: objs,
		Release: rel, LTO: rel, Debug: true}
	if target.OS == types.OSWindows {
		def := filepath.Join(dir, "kernel32.def")
		text := "LIBRARY kernel32.dll\nEXPORTS\n" + strings.Join(kernel, "\n") + "\n"
		if err := os.WriteFile(def, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		lib := filepath.Join(dir, "kernel32.lib")
		runTool(t, tool(bin, "llvm-lib"), "/def:"+def, "/machine:x64", "/out:"+lib)
		job.Format, job.Output, job.Libs = toolchain.COFF, filepath.Join(dir, "t.exe"), []string{lib}
	}
	if err := tools.Link(ctx, job); err != nil {
		t.Fatal(err)
	}
	return job.Output
}

// TestDebugExeDeterministic links the same program into two directories,
// dev and release, for both formats: the executables are identical (the
// COFF one names its PDB by base name and hashes its GUID).
func TestDebugExeDeterministic(t *testing.T) {
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(target.Name+"/"+strconv.Itoa(int(prof)), func(t *testing.T) {
				if target.OS == types.OSWindows && runtime.GOOS != "windows" {
					t.Skip("lld-link's PDB on a Windows host only")
				}
				a := debugExe(t, target, prof, t.TempDir())
				b := debugExe(t, target, prof, t.TempDir())
				ab, err := os.ReadFile(a)
				if err != nil {
					t.Fatal(err)
				}
				bb, err := os.ReadFile(b)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(ab, bb) {
					t.Fatalf("two links in two directories differ (%d and %d bytes)", len(ab), len(bb))
				}
				if target.OS == types.OSWindows {
					if _, err := os.Stat(strings.TrimSuffix(a, ".exe") + ".pdb"); err != nil {
						t.Errorf("no PDB beside the executable: %v", err)
					}
					if bytes.Contains(ab, []byte(filepath.Dir(a))) {
						t.Error("the executable names its directory")
					}
				}
			})
		}
	}
}

// debuggerRun runs argv with a hard deadline (an unhit breakpoint must not
// hang the test) and returns its combined output.
func debuggerRun(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = waitDelay
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s did not finish in 90 s:\n%s", filepath.Base(argv[0]), out.String())
	}
	if err != nil {
		t.Logf("%s: %v", filepath.Base(argv[0]), err)
	}
	return out.String()
}

// cdbPath is the Windows Kits console debugger, or "".
func cdbPath() string {
	for _, root := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if root == "" {
			continue
		}
		p := filepath.Join(root, "Windows Kits", "10", "Debuggers", "x64", "cdb.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("cdb.exe"); err == nil {
		return p
	}
	return ""
}

// TestDebuggerSmokeWindows runs the dev and release programs under cdb
// (Windows Kits) and lldb (LLVM's): break in Probe, go up to Work, print
// the local x (42), from the PDB. Each debugger missing is a skip with the
// reason.
func TestDebuggerSmokeWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Windows debuggers run on a Windows host only")
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		dir := t.TempDir()
		exe := debugExe(t, types.X86_64Windows, prof, dir)
		t.Run("cdb/"+strconv.Itoa(int(prof)), func(t *testing.T) {
			cdb := cdbPath()
			if cdb == "" {
				t.Skip("cdb.exe (Windows Kits 10 Debuggers) is not installed")
			}
			// bm, not bp: at -O3 Probe exists only inlined into Work, and
			// cdb sets a breakpoint on an inline site through bm.
			out := debuggerRun(t, dir, cdb, "-y", dir, "-lines", "-c",
				"bm t!main::Probe; g; k; .frame 1; dv /t; q", exe)
			if !strings.Contains(out, "Breakpoint 0 hit") || !strings.Contains(out, "t!main::Work") ||
				!strings.Contains(out, `[main\t.mk @ 5]`) {
				t.Errorf("cdb did not stop in Probe called from Work at t.mk line 5:\n%s", out)
			}
			if prof == types.Release {
				// At -O3 Probe exists only inlined into Work: the stop is
				// the inline site (S_INLINESITE), its frame an inline one,
				// and x is optimized away while y (Probe's argument) lives.
				if !strings.Contains(out, "(Inline Function)") || !strings.Contains(out, "int64 y = 0n43") {
					t.Errorf("cdb did not show the inline frame and y = 43:\n%s", out)
				}
				return
			}
			for _, want := range []string{"int64 pa = 0n14", "int64 x = 0n42", "int64 y = 0n43"} {
				if !strings.Contains(out, want) {
					t.Errorf("cdb did not print %q:\n%s", want, out)
				}
			}
		})
		t.Run("lldb/"+strconv.Itoa(int(prof)), func(t *testing.T) {
			lldb := filepath.Join(needLLVM(t), "lldb.exe")
			if _, err := os.Stat(lldb); err != nil {
				t.Skip("lldb.exe is not in the LLVM install")
			}
			if why := lldbStarts(lldb); why != "" {
				t.Skip(why)
			}
			out := debuggerRun(t, dir, lldb, "-b", "-o", "breakpoint set -n Probe", "-o", "run", "-o", "up",
				"-o", "frame variable x", "-o", "kill", exe)
			if prof == types.Release {
				if !strings.Contains(out, "Probe") {
					t.Errorf("lldb did not stop in Probe:\n%s", out)
				}
				return
			}
			if !regexp.MustCompile(`\(int\) x = 42`).MatchString(out) {
				t.Errorf("lldb did not print x = 42:\n%s", out)
			}
		})
	}
}

// TestDebugSymbolize is the profiler criterion: in a release --debug
// image, an address inside Work resolves to Work (llvm-symbolizer, the
// symbolizer perf and most profilers share) on both formats.
func TestDebugSymbolize(t *testing.T) {
	bin := needLLVM(t)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			if target.OS == types.OSWindows && runtime.GOOS != "windows" {
				t.Skip("lld-link's PDB on a Windows host only")
			}
			dir := t.TempDir()
			exe := debugExe(t, target, types.Release, dir)
			addr := symbolAddr(t, bin, exe, "mk.main.Work")
			out := runTool(t, tool(bin, "llvm-symbolizer"), "--obj="+exe, "--functions=short",
				"0x"+strconv.FormatUint(addr+4, 16))
			if !strings.Contains(out, "Work") {
				t.Errorf("address 0x%x+4 resolves to %q, want Work", addr, out)
			}
			if !strings.Contains(out, "t.mk") {
				t.Errorf("address 0x%x+4 has no source line: %q", addr, out)
			}
		})
	}
}

// lldbStarts is "" when lldb --version runs, else why it cannot (the LLVM
// Windows installer's lldb.exe needs a Python DLL it does not ship:
// STATUS_DLL_NOT_FOUND, 0xC0000135).
func lldbStarts(lldb string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, lldb, "--version").CombinedOutput()
	if err == nil {
		return ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && uint32(ee.ExitCode()) == 0xC0000135 {
		return "lldb.exe does not start: a DLL it needs is missing (STATUS_DLL_NOT_FOUND; the LLVM installer's lldb needs a matching python3xx.dll)"
	}
	return fmt.Sprintf("lldb.exe --version fails: %v: %s", err, out)
}

// symbolAddr is the virtual address of symbol in exe: from llvm-nm on ELF,
// from the PDB's public symbols (llvm-pdbutil) and the section table on
// COFF, whose image has no symbol table.
func symbolAddr(t *testing.T, bin, exe, symbol string) uint64 {
	t.Helper()
	if strings.HasSuffix(exe, ".exe") {
		return pdbSymbolAddr(t, bin, exe, symbol)
	}
	out := runTool(t, tool(bin, "llvm-nm"), exe)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[2] == symbol {
			v, err := strconv.ParseUint(f[0], 16, 64)
			if err == nil {
				return v
			}
		}
	}
	t.Fatalf("no %s in llvm-nm of %s:\n%s", symbol, exe, out)
	return 0
}

// pdbSymbolAddr is symbol's address in a COFF image: the S_PUB32 record's
// section:offset in the PDB beside it, plus the section's virtual address
// and the image base.
func pdbSymbolAddr(t *testing.T, bin, exe, symbol string) uint64 {
	t.Helper()
	out := runTool(t, tool(bin, "llvm-pdbutil"), "dump", "-publics", strings.TrimSuffix(exe, ".exe")+".pdb")
	m := regexp.MustCompile("`" + regexp.QuoteMeta(symbol) + "`\\s+flags = [^\\n]*addr = ([0-9A-Fa-f]{4}):([0-9A-Fa-f]{4,8})").FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no public %s in the PDB:\n%s", symbol, out)
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

// TestDebuggerSmokeLinux runs the dev and release programs under gdb, or
// lldb when gdb is missing, in WSL (or natively on a Linux host): break in
// Probe, go up to Work, print its locals from the DWARF. Linux leaves run
// only with MK_LINUX=1, under the WSL lock with -p 1; no
// debugger in the distribution is a skip with the reason.
func TestDebuggerSmokeLinux(t *testing.T) {
	prefix, why := hostRunner(types.X86_64Linux)
	if why != "" {
		skipRunner(t, why)
	}
	linux := func(t *testing.T, argv ...string) (string, error) {
		t.Helper()
		argv = append(append([]string(nil), prefix...), argv...)
		if len(prefix) > 0 {
			wslGuard()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.WaitDelay = waitDelay
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		if ctx.Err() != nil && len(prefix) > 0 {
			wslDead(ctx, argv)
			t.Fatalf("%q did not finish in 120 s:\n%s", argv, out.String())
		}
		return out.String(), err
	}
	found, _ := linux(t, "sh", "-c", "command -v gdb || command -v lldb || true")
	dbg := strings.TrimSpace(found)
	if i := strings.IndexByte(dbg, '\n'); i >= 0 {
		dbg = dbg[:i]
	}
	if dbg == "" {
		t.Skip("neither gdb nor lldb is installed in the Linux environment (WSL: sudo apt install gdb)")
	}
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(filepath.Base(dbg)+"/"+strconv.Itoa(int(prof)), func(t *testing.T) {
			exe := debugExe(t, types.X86_64Linux, prof, t.TempDir())
			path := exe
			if len(prefix) > 0 {
				path = wslPath(exe)
			}
			var argv []string
			if filepath.Base(dbg) == "gdb" {
				argv = []string{dbg, "-nx", "-batch", "-ex", "break t.mk:11", "-ex", "run", "-ex", "bt",
					"-ex", "up", "-ex", "info locals", "-ex", "info args", "-ex", "kill", path}
			} else {
				argv = []string{dbg, "-b", "-o", "breakpoint set -n Probe", "-o", "run", "-o", "bt", "-o", "up",
					"-o", "frame variable", "-o", "kill", path}
			}
			out, err := linux(t, argv...)
			if err != nil {
				t.Logf("%s: %v", filepath.Base(dbg), err)
			}
			if !strings.Contains(out, "Work") || !strings.Contains(out, "t.mk:5") {
				t.Errorf("%s did not stop in Probe called from Work at t.mk:5:\n%s", filepath.Base(dbg), out)
			}
			want := []string{"x = 42", "y = 43", "pa = 14"}
			if prof == types.Release {
				// x is optimized away at -O3; y, Probe's inlined argument,
				// lives.
				want = []string{"y = 43"}
			}
			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Errorf("%s did not print %q:\n%s", filepath.Base(dbg), w, out)
				}
			}
		})
	}
}
