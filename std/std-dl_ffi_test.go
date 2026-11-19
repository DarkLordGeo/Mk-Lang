// Why: std.dl loads libraries at run time, where search order, errors and
// cross-task use are easy to get wrong and hard to see.
// What: TestFFI_Windows, TestFFI_Imports, TestFFI_LinuxStatic,
// TestFFI_LinuxUnsupported and TestFFI_Module.
// How: Windows programs open libraries under each Search, call through looked-up
// addresses and use tasks, with Go's syscall.LoadLibrary as oracle for absolute paths.
//
// A minimal program's import table adds only the four loader names to hello
// world's. A Linux build is static, loads no libc and names no dlopen; run
// under the WSL lock, Open answers Unsupported. No test writes C or imports
// unsafe.

package std

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// dlPkg is the manifest of the test package: its root module holds the
// cookbook's unsafe call form, so it is listed.
const dlPkg = "const Package = pkg.Package(\n    name: \"p\",\n    bins: [pkg.Bin(name: \"p\", root: \".\", link_script: \"\", cpu: \"\", subsystem: \"console\")],\n    deps: [],\n    c_sources: [],\n" +
	"    c_imports: [],\n    asm_sources: [],\n    targets: [],\n    unsafe_modules: [\".\"],\n    links: [],\n)\n"

// dlBuild writes src as package p's main.mk beside a copy of this tree's
// std, builds it with flags into out (a path relative to the temp root,
// ".exe" added for a Windows build) and returns the executable's path.
func dlBuild(t *testing.T, root, out, src string, flags ...string) string {
	t.Helper()
	stdDir := filepath.Join(root, "std")
	if _, err := os.Stat(stdDir); err != nil {
		dlStd(t, root)
	}
	pkgDir := filepath.Join(root, "p")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"main.mk": src, "pkg.mk": dlPkg} {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(root, filepath.FromSlash(out))
	if runtime.GOOS == "windows" && !slices.Contains(flags, "x86_64-linux") {
		exe += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MK_CACHE", filepath.Join(root, "cache"))
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, ".")
	text, status := mkCmd(t, pkgDir, stdDir, args...)
	if status != 0 {
		if strings.Contains(text, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", text)
		}
		t.Fatalf("mk build %v: exit %d\n%s", flags, status, text)
	}
	return exe
}

// dlFiles are the files of this phase, relative to std/: overlaid on
// MK_DL_BASE's snapshot.
var dlFiles = []string{"dl/dl.mk", "sys/windows/dl.mk", "sys/linux/dl.mk", "sys/nutshell/dl.mk"}

// dlStd makes root/std: a copy of this tree's std, or, when MK_DL_BASE
// names a snapshot of a whole std (for a run while other std files are
// mid-edit), a copy of it with this phase's files laid over and
// kernel32.mk's FreeLibrary extern added if the snapshot lacks it.
func dlStd(t *testing.T, root string) {
	t.Helper()
	base := os.Getenv("MK_DL_BASE")
	if base == "" {
		copyStd(t, root, nil)
		return
	}
	stdDir := filepath.Join(root, "std")
	if err := os.CopyFS(stdDir, os.DirFS(base)); err != nil {
		t.Fatal(err)
	}
	for _, p := range dlFiles {
		dst := filepath.Join(stdDir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(readText(t, p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k := filepath.Join(stdDir, "sys", "windows", "kernel32.mk")
	b, err := os.ReadFile(k)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("extern FreeLibrary(")) {
		b = append(b, []byte("extern FreeLibrary(module: uint): i32\n")...)
		if err := os.WriteFile(k, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// dlProbe exercises every std.dl line on Windows. Its arguments: 1 a text
// file named .dll, 2 an absent absolute path, 3 the absolute path of a DLL
// only in the working directory, then names of 1,024 and 1,025 UTF-16 units
// in ASCII, 1,024 units of two-byte UTF-8, and 1,024 and 1,026 units of
// surrogate pairs, then a 256-byte and a 255-byte symbol name, then the
// absolute path of a directory named .dll (Os: ERROR_ACCESS_DENIED).
const dlProbe = `import std.dl
import std.raw

// Show opens name under search and prints label and the error, or ok after
// closing the library.
Show(sys: &System, label: str, name: str, search: dl.Search): WriteError!void {
    lib := dl.Open(sys.io, name, search) else e {
        try sys.Print("{l}: {e}\n", (l: label, e: e))
        return
    }
    lib.Close(sys.io)
    try sys.Print("{l}: ok\n", (l: label))
}

// ScreenWidth is the cookbook's "Load a library at run time" block.
ScreenWidth(io: Io): dl.Error!int {
    lib := try dl.Open(io, "user32.dll", dl.Search.System)
    defer lib.Close(io)
    var width := 0
    if p := lib.Symbol("GetSystemMetrics") {
        var w := u64(raw.Addr(p))
        unsafe "GetSystemMetrics takes and returns an i32, and lib is open" {
            f := raw.Read(raw.PtrCast[u64, fn(i32): i32](raw.OfMut(mut w)))
            width = int(f(0))
        }
    }
    return width
}

// Pid calls kernel32's GetCurrentProcessId through an address from dl.
Pid(io: Io): dl.Error!int {
    lib := try dl.Open(io, "kernel32.dll", dl.Search.System)
    defer lib.Close(io)
    var pid := 0
    if p := lib.Symbol("GetCurrentProcessId") {
        var w := u64(raw.Addr(p))
        unsafe "GetCurrentProcessId takes nothing and returns a u32" {
            f := raw.Read(raw.PtrCast[u64, fn(): u32](raw.OfMut(mut w)))
            pid = int(f())
        }
    }
    return pid
}

// Heavy opens shell32.dll, whose initializers are deep, from a task.
Heavy(io: Io) {
    lib := dl.Open(io, "shell32.dll", dl.Search.System) else {
        Panic("shell32.dll did not open")
    }
    Assert(lib.Symbol("ShellExecuteW").IsSome())
    lib.Close(io)
}

// Moved closes a library another task opened (Library is Send).
Moved(io: Io, lib: dl.Library) {
    Assert(lib.Symbol("GetFileVersionInfoSizeW").IsSome())
    lib.Close(io)
}

// Kept detaches a library; its address stays valid.
Kept(io: Io): dl.Error!bool {
    lib := try dl.Open(io, "version.dll", dl.Search.System)
    found := lib.Symbol("GetFileVersionInfoSizeW").IsSome()
    lib.Detach()
    return found
}

export Main(sys: &System): WriteError!void {
    io := sys.io
    try Show(sys, "system", "version.dll", dl.Search.System)
    try Show(sys, "application finds System32", "version.dll", dl.Search.Application)
    try Show(sys, "application beside the executable", "mkdlpriv.dll", dl.Search.Application)
    try Show(sys, "system misses the executable's directory", "mkdlpriv.dll", dl.Search.System)
    try Show(sys, "missing", "mk-no-such-library.dll", dl.Search.System)
    try Show(sys, "empty", "", dl.Search.System)
    try Show(sys, "slash", "a/version.dll", dl.Search.System)
    try Show(sys, "backslash", "a\\version.dll", dl.Search.Application)
    try Show(sys, "relative path", "version.dll", dl.Search.Path)
    try Show(sys, "drive-relative path", "C:version.dll", dl.Search.Path)
    try Show(sys, "drive-relative system", "C:version.dll", dl.Search.System)
    try Show(sys, "drive-relative application", "V:mkdlcwd.dll", dl.Search.Application)
    try Show(sys, "rooted path", "\\Windows\\System32\\version.dll", dl.Search.Path)
    try Show(sys, "nul", "ver\u{0}sion.dll", dl.Search.System)
    try Show(sys, "not a library", sys.args[1], dl.Search.Path)
    try Show(sys, "absent path", sys.args[2], dl.Search.Path)
    try Show(sys, "cwd path", sys.args[3], dl.Search.Path)
    try Show(sys, "cwd system", "mkdlcwd.dll", dl.Search.System)
    try Show(sys, "cwd application", "mkdlcwd.dll", dl.Search.Application)
    try Show(sys, "1024 ascii", sys.args[4], dl.Search.System)
    try Show(sys, "1025 ascii", sys.args[5], dl.Search.System)
    try Show(sys, "1024 two-byte", sys.args[6], dl.Search.System)
    try Show(sys, "1024 pairs", sys.args[7], dl.Search.System)
    try Show(sys, "1026 pairs", sys.args[8], dl.Search.System)
    try Show(sys, "forward slashes", "C:/Windows/System32/version.dll", dl.Search.Path)
    try Show(sys, "a directory", sys.args[11], dl.Search.Path)
    lib := dl.Open(io, "version.dll", dl.Search.System) else e {
        try sys.Print("open: {e}\n", (e: e))
        return
    }
    found := lib.Symbol("GetFileVersionInfoSizeW").IsSome()
    missing := lib.Symbol("NoSuchExport").IsNone()
    empty := lib.Symbol("").IsNone()
    nul := lib.Symbol("GetFileVersionInfoSizeW\u{0}").IsNone()
    over := lib.Symbol(sys.args[9]).IsNone()
    most := lib.Symbol(sys.args[10]).IsNone()
    scope s := io.Scope() {
        s.go Heavy(io)
        s.go Moved(io, lib)
    }
    try sys.Print("tasks done\n")
    try sys.Print("symbol found: {b}\n", (b: found))
    try sys.Print("symbol missing: {b}\n", (b: missing))
    try sys.Print("symbol empty: {b}\n", (b: empty))
    try sys.Print("symbol nul: {b}\n", (b: nul))
    try sys.Print("symbol 256: {b}\n", (b: over))
    try sys.Print("symbol 255: {b}\n", (b: most))
    w := ScreenWidth(io) else e {
        try sys.Print("width: {e}\n", (e: e))
        return
    }
    try sys.Print("width read: {b}\n", (b: w >= 0))
    pid := Pid(io) else e {
        try sys.Print("pid: {e}\n", (e: e))
        return
    }
    try sys.Print("pid {p}\n", (p: pid))
    kept := Kept(io) else e {
        try sys.Print("detach: {e}\n", (e: e))
        return
    }
    try sys.Print("detach: {k}\n", (k: kept))
}
`

// dlWant is dlProbe's output on Windows, the pid line aside.
const dlWant = `system: ok
application finds System32: ok
application beside the executable: ok
system misses the executable's directory: NotFound
missing: NotFound
empty: BadName
slash: BadName
backslash: BadName
relative path: BadName
drive-relative path: BadName
drive-relative system: BadName
drive-relative application: BadName
rooted path: BadName
nul: BadName
not a library: Invalid
absent path: NotFound
cwd path: ok
cwd system: NotFound
cwd application: NotFound
1024 ascii: NotFound
1025 ascii: BadName
1024 two-byte: NotFound
1024 pairs: NotFound
1026 pairs: BadName
forward slashes: ok
a directory: Os(code: 5)
tasks done
symbol found: true
symbol missing: true
symbol empty: true
symbol nul: true
symbol 256: true
symbol 255: true
width read: true
pid PID
detach: true
`

// TestFFI_Windows tests the search modes, that the working directory is
// not searched, the errors and the cookbook recipe's call form, in dev and
// release. The private DLL is a
// copy of System32's version.dll under another name (no C is built): one
// copy beside the executable, one only in the working directory.
func TestFFI_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("std.dl loads libraries on Windows only outside system-libc mode")
	}
	root := t.TempDir()
	sysDir := filepath.Join(os.Getenv("SystemRoot"), "System32")
	version, err := os.ReadFile(filepath.Join(sysDir, "version.dll"))
	if err != nil {
		t.Skipf("no System32 version.dll: %v", err)
	}
	cwd := filepath.Join(root, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "fake.dll")
	cwdDll := filepath.Join(cwd, "mkdlcwd.dll")
	for p, b := range map[string][]byte{fake: []byte("not a library\n"), cwdDll: version} {
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{fake, filepath.Join(root, "absent", "mk-absent.dll"), cwdDll,
		strings.Repeat("a", 1020) + ".dll", strings.Repeat("a", 1021) + ".dll",
		strings.Repeat("é", 1020) + ".dll", strings.Repeat("😀", 510) + ".dll", strings.Repeat("😀", 511) + ".dll",
		strings.Repeat("G", 256), strings.Repeat("G", 255), filepath.Join(root, "dir.dll")}
	if err := os.MkdirAll(filepath.Join(root, "dir.dll"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, prof := range []struct{ name, out string }{{"dev", "dev/p"}, {"release", "release/p"}} {
		t.Run(prof.name, func(t *testing.T) {
			var flags []string
			if prof.name == "release" {
				flags = []string{"--release"}
			}
			exe := dlBuild(t, root, prof.out, dlProbe, flags...)
			if err := os.WriteFile(filepath.Join(filepath.Dir(exe), "mkdlpriv.dll"), version, 0o644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, args...)
			cmd.Dir = cwd
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("%s: %v\nstdout:\n%s\nstderr:\n%s", exe, err, out.String(), errb.String())
			}
			got := regexp.MustCompile(`(?m)^pid [1-9][0-9]*$`).ReplaceAllString(out.String(), "pid PID")
			// A name of 1,024 units is valid; Windows without long path
			// support answers ERROR_FILENAME_EXCED_RANGE (206), with it
			// ERROR_MOD_NOT_FOUND. Either way not BadName.
			got = regexp.MustCompile(`(?m)^(1024 [a-z-]+): Os\(code: 206\)$`).ReplaceAllString(got, "$1: NotFound")
			if got != dlWant {
				t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", got, dlWant, errb.String())
			}
		})
	}

	// The oracle: Go's LoadLibrary searches its own way, so only absolute
	// paths, where the search flags cannot change the answer, are compared.
	for _, c := range []struct {
		path string
		want string
	}{{fake, "Invalid"}, {filepath.Join(root, "absent", "mk-absent.dll"), "NotFound"}, {cwdDll, "ok"}} {
		got := dlGoLoad(t, c.path)
		if got != c.want {
			t.Errorf("Go's LoadLibrary(%s) is %s; std.dl's line says %s", c.path, got, c.want)
		}
	}
}

// dlMinimal opens one library, looks one name up and closes it.
const dlMinimal = `import std.dl

export Main(sys: &System): WriteError!void {
    lib := dl.Open(sys.io, "version.dll", dl.Search.System) else e {
        try sys.Print("open: {e}\n", (e: e))
        return
    }
    found := lib.Symbol("GetFileVersionInfoSizeW").IsSome()
    lib.Close(sys.io)
    try sys.Print("symbol: {b}\n", (b: found))
}
`

// dlHello is tests/programs/hello.mk's program.
const dlHello = `export Main(sys: &System): WriteError!void {
    try sys.Print("hello {name}, {n} args\n", (name: "world", n: sys.args.Len()))
}
`

// peImports is the set of name:dll entries of exe's import table, the DLL
// lower-cased.
func peImports(t *testing.T, exe string) []string {
	t.Helper()
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.ImportedSymbols()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range syms {
		name, dll, _ := strings.Cut(s, ":")
		out = append(out, name+":"+strings.ToLower(dll))
	}
	slices.Sort(out)
	return out
}

// TestFFI_Imports, in dev and release: a binary that uses
// std.dl imports, beyond hello world's set, only kernel32's LoadLibraryExW,
// GetProcAddress, FreeLibrary and GetLastError; hello world imports no
// FreeLibrary, so the extern added for std.dl moves nothing it links.
func TestFFI_Imports(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("reads a Windows import table built on Windows")
	}
	root := t.TempDir()
	allowed := []string{"FreeLibrary:kernel32.dll", "GetLastError:kernel32.dll", "GetProcAddress:kernel32.dll", "LoadLibraryExW:kernel32.dll"}
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			var flags []string
			if prof == "release" {
				flags = []string{"--release"}
			}
			hello := peImports(t, dlBuild(t, root, prof+"/hello", dlHello, flags...))
			exe := dlBuild(t, root, prof+"/dl", dlMinimal, flags...)
			uses := peImports(t, exe)
			if slices.Contains(hello, "FreeLibrary:kernel32.dll") {
				t.Errorf("hello world imports FreeLibrary: %v", hello)
			}
			for _, s := range hello {
				if !slices.Contains(uses, s) {
					t.Errorf("the std.dl program lacks hello world's import %s", s)
				}
			}
			for _, s := range uses {
				if !slices.Contains(hello, s) && !slices.Contains(allowed, s) {
					t.Errorf("the std.dl program imports %s beyond hello world's set and the loader's four", s)
				}
			}
			for _, s := range []string{"FreeLibrary:kernel32.dll", "LoadLibraryExW:kernel32.dll", "GetProcAddress:kernel32.dll"} {
				if !slices.Contains(uses, s) {
					t.Errorf("the std.dl program does not import %s", s)
				}
			}
			if fi, err := os.Stat(exe); err == nil {
				t.Logf("%s: the std.dl program is %d bytes", prof, fi.Size())
			}
			r := runNative(t, exe, "")
			if r.code != 0 || r.stdout != "symbol: true\n" {
				t.Errorf("exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
			}
		})
	}
}

// dlLinux is the Linux program: Open answers Unsupported, Symbol is not
// reached.
const dlLinux = `import std.dl

export Main(sys: &System): WriteError!void {
    lib := dl.Open(sys.io, "libm.so.6", dl.Search.System) else e {
        try sys.Print("open: {e}\n", (e: e))
        return
    }
    found := lib.Symbol("cos").IsSome()
    lib.Close(sys.io)
    try sys.Print("symbol: {b}\n", (b: found))
}
`

// TestFFI_LinuxStatic is the static Linux build's build half, with no wsl.exe:
// a Linux binary that uses std.dl, dev and release, has no PT_INTERP and
// no PT_DYNAMIC, and names no dlopen.
func TestFFI_LinuxStatic(t *testing.T) {
	root := t.TempDir()
	for _, prof := range []string{"dev", "release"} {
		t.Run(prof, func(t *testing.T) {
			flags := []string{"--target", "x86_64-linux"}
			if prof == "release" {
				flags = append(flags, "--release")
			}
			exe := dlBuild(t, root, prof+"/dl", dlLinux, flags...)
			dlStaticELF(t, exe)
		})
	}
}

// dlStaticELF fails t unless exe is a static ELF that names no dlopen.
func dlStaticELF(t *testing.T, exe string) {
	t.Helper()
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
			t.Errorf("%s has a %v segment", exe, p.Type)
		}
	}
	if syms, err := f.Symbols(); err == nil {
		for _, s := range syms {
			if strings.Contains(s.Name, "dlopen") {
				t.Errorf("%s has the symbol %s", exe, s.Name)
			}
		}
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("dlopen")) {
		t.Errorf("%s holds the bytes dlopen", exe)
	}
}

// TestFFI_LinuxUnsupported is the static Linux build's run half: on
// x86_64-linux outside system-libc mode Open answers Unsupported, in dev
// and release. Both binaries run from the distribution's /tmp in one
// wsl.exe client; MK_LINUX=1 under tools/wsl-lock.ps1 only.
func TestFFI_LinuxUnsupported(t *testing.T) {
	linuxGate(t)
	root := t.TempDir()
	var script strings.Builder
	for _, prof := range []string{"dev", "release"} {
		flags := []string{"--target", "x86_64-linux"}
		if prof == "release" {
			flags = append(flags, "--release")
		}
		exe := dlBuild(t, root, prof+"/dl", dlLinux, flags...)
		script.WriteString(linuxScratchSh("cp '" + rtsWSLPath(exe) + "' \"$MK_LINUX_TMP/dl\" && chmod +x \"$MK_LINUX_TMP/dl\" && cd \"$MK_LINUX_TMP\" && ./dl"))
		script.WriteString("echo \"" + prof + " exit $s\"\n")
	}
	got := linuxRunScript(t, root, "dl.sh", script.String(), 60*time.Second)
	want := "open: Unsupported\ndev exit 0\nopen: Unsupported\nrelease exit 0\n"
	if got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
}

// TestFFI_Module: std/dl holds no unsafe, and the three std.sys
// loader files export the same declarations, so std.dl checks on every
// hosted target (nutshell_sys_test.go covers Linux and Nutshell; this adds
// Windows).
func TestFFI_Module(t *testing.T) {
	ents, err := os.ReadDir("dl")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		for i, line := range strings.Split(readText(t, "dl/"+e.Name()), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if regexp.MustCompile(`\bunsafe\b`).MatchString(code) {
				t.Errorf("std/dl/%s:%d: %s; std.dl holds no unsafe", e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
	exports := func(dir string) []string {
		var out []string
		for _, line := range strings.Split(readText(t, dir+"/dl.mk"), "\n") {
			if strings.HasPrefix(line, "export ") {
				out = append(out, strings.TrimSuffix(line, " {"))
			}
		}
		return out
	}
	win := exports("sys/windows")
	if len(win) != 5 {
		t.Errorf("std/sys/windows/dl.mk exports %d declarations, want 5: %q", len(win), win)
	}
	for _, dir := range []string{"sys/linux", "sys/nutshell"} {
		if got := exports(dir); !slices.Equal(got, win) {
			t.Errorf("std/%s/dl.mk exports\n%q\nwant Windows'\n%q", dir, got, win)
		}
	}
}
