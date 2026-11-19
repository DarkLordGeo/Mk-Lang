// Why: the whole toolchain path (probe, compile, import library, link, run)
// must produce a working executable with no C runtime on Windows and Linux.
// What: TestSmokeWindowsNoCRT and TestSmokeLinuxStatic, the helpers
// smokeBuild and smokeRun, and the Linux guards linuxRun and wslGuard.
// How: builds a hand-written LLVM module from testdata, checks its imports
// or program headers, builds it twice for byte identity, and runs it.

package toolchain

import (
	"bytes"
	"context"
	"debug/elf"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const smokeOut = "hello from mk\n"
const smokeExit = 7

type smokeVariant struct {
	name string
	opt  OptLevel
	lto  bool
}

var smokeVariants = []smokeVariant{{"O0", O0, false}, {"O3", O3, false}, {"O3-thinlto", O3, true}}

// smokeBuild compiles testdata/<file> for the triple through Probe,
// Compile, ImportLib (Windows) and Link into dir and returns the executable.
func smokeBuild(t *testing.T, tl *Tools, triple, file string, v smokeVariant, dir string) string {
	t.Helper()
	ctx := context.Background()
	facts, err := tl.Probe(ctx, triple)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	ll := filepath.Join(dir, "smoke.ll")
	writeFile(t, ll, facts.Header()+string(body))
	obj := filepath.Join(dir, "smoke.o")
	if err := tl.Compile(ctx, CompileJob{Triple: triple, Input: ll, Output: obj, Opt: v.opt, LTO: v.lto, CPU: "x86-64-v2"}); err != nil {
		t.Fatal(err)
	}
	job := LinkJob{Format: FormatOf(triple), Objects: []string{obj}, Release: v.opt == O3, LTO: v.lto}
	if v.lto {
		job.LTOCacheDir = filepath.Join(dir, "thinlto")
		job.LTOCacheSize = 64 << 20
		job.Jobs = 2
	}
	if job.Format == COFF {
		def, err := os.ReadFile(filepath.Join("testdata", "kernel32.def"))
		if err != nil {
			t.Fatal(err)
		}
		names, err := ParseDefList(string(def))
		if err != nil {
			t.Fatal(err)
		}
		lib := filepath.Join(dir, "kernel32.lib")
		if err := tl.ImportLib(ctx, "kernel32.dll", names, "x64", lib); err != nil {
			t.Fatal(err)
		}
		job.Libs = []string{lib}
		job.Output = filepath.Join(dir, "smoke.exe")
	} else {
		job.Output = filepath.Join(dir, "smoke")
	}
	if err := tl.Link(ctx, job); err != nil {
		t.Fatal(err)
	}
	return job.Output
}

// smokeRun runs exe through the triple's runner; ok=false when this host
// has none.
func smokeRun(t *testing.T, triple, exe string) (out string, code int, ok bool) {
	t.Helper()
	prefix, ok := Runner(triple)
	if !ok {
		return "", 0, false
	}
	if len(prefix) > 0 {
		if !linuxRun() {
			t.Skipf("%s (built and checked %s; its run starts wsl.exe)", linuxOff, exe)
		}
		wslGuard()
	}
	argv := append(append([]string(nil), prefix...), HostPathFor(prefix, exe))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatalf("%q: %v", argv, err)
	}
	if stderr.Len() > 0 {
		t.Logf("%s stderr: %s", filepath.Base(exe), stderr.String())
	}
	return stdout.String(), code, true
}

// linuxOff is the skip reason of a leaf that would start wsl.exe while
// MK_LINUX=1 is not set; the gate runs those leaves in their own pass, under
// the WSL lock.
const linuxOff = "MK_LINUX=1 not set"

// linuxRun reports whether this run may start wsl.exe, MK_LINUX=1.
func linuxRun() bool { return os.Getenv("MK_LINUX") == "1" }

// wslGuard panics when a wsl.exe is about to start without MK_LINUX=1: a
// leaf that reaches it missed its linuxRun check.
//
// With MK_LINUX=1 it also panics unless the run holds the WSL lock
// (tools/wsl-lock.ps1 sets MK_WSL_LOCKED=1), so a Linux run outside the
// lock cannot happen by accident; MK_WSL_UNLOCKED_OK=1 allows one on
// purpose.
func wslGuard() {
	if !linuxRun() {
		panic("wsl.exe started without MK_LINUX=1; the leaf must skip with " + linuxOff)
	}
	if os.Getenv("MK_WSL_LOCKED") != "1" && os.Getenv("MK_WSL_UNLOCKED_OK") != "1" {
		panic("wsl.exe started with MK_LINUX=1 outside the WSL lock (MK_WSL_LOCKED is not 1): run under pwsh -NoProfile -File tools/wsl-lock.ps1, or set MK_WSL_UNLOCKED_OK=1")
	}
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func sameBytes(t *testing.T, a, b string) bool {
	t.Helper()
	x, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

// TestSmokeWindowsNoCRT: a CRT-free Windows program built through
// toolchain imports exactly kernel32.dll, builds byte-identically twice
// and on a Windows host prints its line and exits 7.
func TestSmokeWindowsNoCRT(t *testing.T) {
	tl := realTools(t)
	const triple = "x86_64-pc-windows-msvc"
	if miss := tl.Need(triple); len(miss) > 0 {
		t.Skipf("SKIP: %s lacks %v", tl.Dir, miss)
	}
	for _, v := range smokeVariants {
		t.Run(v.name, func(t *testing.T) {
			exe := smokeBuild(t, tl, triple, "smoke_windows.ll", v, t.TempDir())
			again := smokeBuild(t, tl, triple, "smoke_windows.ll", v, t.TempDir())
			if !sameBytes(t, exe, again) {
				t.Error("two builds differ")
			}
			libs := importsOf(t, exe)
			if len(libs) != 1 || !strings.EqualFold(libs[0], "kernel32.dll") {
				t.Errorf("imports = %q, want exactly kernel32.dll", libs)
			}
			t.Logf("%s %s: %d bytes, imports %q", triple, v.name, fileSize(t, exe), libs)
			out, code, ok := smokeRun(t, triple, exe)
			if !ok {
				t.Skipf("SKIP: built and checked, but no runner for %s on %s", triple, runtime.GOOS)
			}
			if out != smokeOut || code != smokeExit {
				t.Errorf("ran: output %q exit %d, want %q exit %d", out, code, smokeOut, smokeExit)
			}
		})
	}
}

// TestSmokeLinuxStatic: a libc-free static Linux program built through
// toolchain has no PT_INTERP and no PT_DYNAMIC, builds byte-identically
// twice, and runs (natively, or through wsl.exe when present) printing its
// line and exiting 7.
func TestSmokeLinuxStatic(t *testing.T) {
	tl := realTools(t)
	const triple = "x86_64-unknown-linux-gnu"
	if miss := tl.Need(triple); len(miss) > 0 {
		t.Skipf("SKIP: %s lacks %v", tl.Dir, miss)
	}
	for _, v := range smokeVariants {
		t.Run(v.name, func(t *testing.T) {
			exe := smokeBuild(t, tl, triple, "smoke_linux.ll", v, t.TempDir())
			again := smokeBuild(t, tl, triple, "smoke_linux.ll", v, t.TempDir())
			if !sameBytes(t, exe, again) {
				t.Error("two builds differ")
			}
			f, err := elf.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			if f.Type != elf.ET_EXEC || f.Machine != elf.EM_X86_64 {
				t.Errorf("ELF type %v machine %v", f.Type, f.Machine)
			}
			for _, p := range f.Progs {
				if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
					t.Errorf("has %v", p.Type)
				}
			}
			f.Close()
			t.Logf("%s %s: %d bytes", triple, v.name, fileSize(t, exe))
			out, code, ok := smokeRun(t, triple, exe)
			if !ok {
				t.Skipf("SKIP: built and checked, but no runner for %s on %s (no wsl.exe)", triple, runtime.GOOS)
			}
			if out != smokeOut || code != smokeExit {
				t.Errorf("ran: output %q exit %d, want %q exit %d", out, code, smokeOut, smokeExit)
			}
		})
	}
}
