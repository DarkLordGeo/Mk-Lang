// Why: the Nutshell targets go through every layer of the driver (target
// selection, std's sys/nutshell, pkg.Select, MK_STD and the ELF link), and
// the component loader accepts only a narrow kind of executable.
// What: mk check of a hosted program for x86_64-nutshell and
// aarch64-nutshell; mk build's static ELF for x86_64-nutshell, checked
// against the loader's rules and byte-identical on a rebuild; the refusal
// of aarch64-nutshell; pkg.Select(os: "nutshell"); MK_STD's sys/nutshell;
// the private Syscall intrinsic lowering to `syscall`; and a match on
// Target.os that must keep a _ arm, since Os grows with each target.
// How: checks and builds in temporary directories and reads the result with
// debug/elf; nothing runs an executable, so no runner and no WSL.

package driver

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// nutshellProgram is a hosted program that reaches std.task, std.rt and
// std.sys through Print.
const nutshellProgram = `export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for i in 1..4 {
        try Print(mut out, "{i}\n", (i))
    }
}
`

func nutshellFile(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(file, []byte(nutshellProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestNutshellCheck(t *testing.T) {
	t.Parallel()
	file := nutshellFile(t)
	// One cache for both targets: every check result is keyed by the
	// target, so each target is still checked for its own.
	cache := t.TempDir()
	for _, tg := range []types.Target{types.X86_64Nutshell, types.Aarch64Nutshell} {
		r := check(t, Config{Root: file, Target: tg, CacheDir: cache})
		if len(r.Report.Diags) > 0 {
			t.Errorf("mk check --target %s:\n%s", tg.Name, r.Report.Text(r.Sources))
		}
	}
}

func TestNutshellBuildRefusesAarch64(t *testing.T) {
	t.Parallel()
	s, err := NewSession(Config{Root: nutshellFile(t), Target: types.Aarch64Nutshell, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "main"))
	// E0202 reason not-built, as every checked-only target.
	if err != nil || len(paths) != 0 || len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0202 ||
		!strings.Contains(r.Report.Diags[0].Diag.Message, "aarch64-nutshell is checked only") {
		t.Errorf("mk build --target aarch64-nutshell: %v, %v, %v", err, paths, r.Report.Diags[0].Diag)
	}
}

// TestNutshellELF: dev and release, the executable is ELF64 LE,
// ET_EXEC, EM_X86_64, entered inside an executable PT_LOAD (at _start in
// dev, where symbols are kept); it has no PT_INTERP, PT_DYNAMIC,
// PT_GNU_RELRO or PT_TLS, at most 32 PT_LOADs, each page-aligned in the file
// and in memory, never both writable and executable, with memsz >= filesz,
// all below 2 GiB; its stack is not executable; dev keeps _start and
// mk_start and has no mk_clone or mk_sigreturn. A second build from an
// empty cache gives the same bytes.
func TestNutshellELF(t *testing.T) {
	t.Parallel()
	file := nutshellFile(t)
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			t.Parallel()
			exe := buildProgram(t, file, types.X86_64Nutshell, prof, t.TempDir())
			checkNutshellELF(t, exe, prof == types.Dev)
			again := buildProgram(t, file, types.X86_64Nutshell, prof, t.TempDir())
			a, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(again)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two builds from empty caches differ (%d and %d bytes)", len(a), len(b))
			}
		})
	}
}

func checkNutshellELF(t *testing.T, exe string, symbols bool) {
	t.Helper()
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_EXEC || f.Machine != elf.EM_X86_64 {
		t.Errorf("class %v data %v type %v machine %v, want ELFCLASS64 ELFDATA2LSB ET_EXEC EM_X86_64", f.Class, f.Data, f.Type, f.Machine)
	}
	loads, entryIn := 0, false
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_INTERP, elf.PT_DYNAMIC, elf.PT_GNU_RELRO, elf.PT_TLS:
			t.Errorf("program header %v", p.Type)
		case elf.PT_GNU_STACK:
			if p.Flags&elf.PF_X != 0 {
				t.Errorf("executable stack: %v", p.Flags)
			}
		case elf.PT_LOAD:
			loads++
			if p.Off%4096 != 0 || p.Vaddr%4096 != 0 {
				t.Errorf("PT_LOAD at offset %#x vaddr %#x is not page-aligned", p.Off, p.Vaddr)
			}
			if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
				t.Errorf("PT_LOAD at %#x is writable and executable", p.Vaddr)
			}
			if p.Memsz < p.Filesz || p.Vaddr+p.Memsz > 1<<31 {
				t.Errorf("PT_LOAD at %#x: filesz %#x memsz %#x", p.Vaddr, p.Filesz, p.Memsz)
			}
			if p.Flags&elf.PF_X != 0 && f.Entry >= p.Vaddr && f.Entry < p.Vaddr+p.Memsz {
				entryIn = true
			}
		}
	}
	if loads == 0 || loads > 32 || !entryIn {
		t.Errorf("%d PT_LOADs; entry %#x in an executable one: %v", loads, f.Entry, entryIn)
	}
	for _, s := range f.Sections {
		if s.Type == elf.SHT_DYNAMIC || s.Type == elf.SHT_RELA || s.Type == elf.SHT_REL || s.Name == ".interp" {
			t.Errorf("section %s (%v)", s.Name, s.Type)
		}
	}
	if !symbols {
		return
	}
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]uint64{}
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF {
			at[s.Name] = s.Value
		}
	}
	if v, ok := at["_start"]; !ok || v != f.Entry {
		t.Errorf("entry %#x, _start %#x (%v)", f.Entry, v, ok)
	}
	for _, n := range []string{"mk_start", "mk_switch", "mk_task_start", "mk_call_system"} {
		if _, ok := at[n]; !ok {
			t.Errorf("%s is not defined", n)
		}
	}
	for _, n := range []string{"mk_clone", "mk_sigreturn"} {
		if _, ok := at[n]; ok {
			t.Errorf("%s is defined", n)
		}
	}
}

// TestPackageSelectsNutshell: a package's pkg.Select(os: "nutshell")
// directory is its module on both Nutshell targets, and not on Linux.
func TestPackageSelectsNutshell(t *testing.T) {
	t.Parallel()
	sel := `[pkg.Select(os: "nutshell", modules: [pkg.Module(name: "os.native", dir: "src/os/nutshell")]), ` +
		`pkg.Select(os: "linux", modules: [pkg.Module(name: "os.native", dir: "src/os/linux")])]`
	dir := writeTree(t, map[string]string{
		"pkg.mk":               manifest(map[string]string{"targets": sel}),
		"src/main.mk":          "import os.native\n\nexport F(): str {\n    return native.Name()\n}\n",
		"src/os/nutshell/n.mk": "export Name(): str {\n    return \"nutshell\"\n}\n",
		"src/os/linux/n.mk":    "export Other(): int {\n    return 1\n}\n",
	})
	for _, tg := range []types.Target{types.X86_64Nutshell, types.Aarch64Nutshell} {
		clean(t, check(t, Config{Root: dir, Target: tg}))
	}
	if r := check(t, Config{Root: dir, Target: types.X86_64Linux}); len(r.Report.Diags) == 0 {
		t.Error("x86_64-linux read src/os/nutshell")
	}
}

// TestMkStdNutshell: MK_STD names the std mk reads, its sys/nutshell
// included: a copy of std whose sys/nutshell lacks ThreadInit, which
// std.task calls, fails mk check for x86_64-nutshell in std.task, and the
// same copy checks clean for x86_64-linux, whose std.sys is sys/linux.
func TestMkStdNutshell(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "std", "sys", "nutshell", "thread.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	cut := "export ThreadInit(): bool {\n    return false\n}\n\n"
	if !strings.Contains(text, cut) {
		t.Fatal("std/sys/nutshell/thread.mk has no ThreadInit to remove")
	}
	stdDir := realStdWith(t, map[string]string{"sys/nutshell/thread.mk": strings.Replace(text, cut, "", 1)})
	t.Setenv("MK_STD", stdDir)
	file := nutshellFile(t)
	r := check(t, Config{Root: file, Target: types.X86_64Nutshell})
	found := false
	for _, l := range lines(r) {
		if strings.Contains(filepath.ToSlash(l), "task/start.mk") {
			found = true
		}
	}
	if !found {
		t.Errorf("MK_STD's sys/nutshell without ThreadInit: %q, want a diagnostic in std/task/start.mk", lines(r))
	}
	clean(t, check(t, Config{Root: file, Target: types.X86_64Linux}))
}

// TestNutshellSyscallLowers: std/sys/nutshell's private Syscall
// declaration is the intrinsic on x86_64-nutshell, so a backend that MK_STD
// puts in its place can make system calls. With std's placeholder, Exit is
// ud2 and no `syscall`; with a copy of std whose Exit calls Syscall, as
// Linux's does, the built Exit holds the `syscall` instruction (0f 05).
func TestNutshellSyscallLowers(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "std", "sys", "nutshell", "sys.mk"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	i := strings.Index(text, "export Exit(code: i32): never {\n")
	j := strings.Index(text, "export Pid(): u64 {")
	if i < 0 || j < i {
		t.Fatal("std/sys/nutshell/sys.mk has no Exit before Pid")
	}
	backend := text[:i] + "export Exit(code: i32): never {\n    unsafe \"system call\" {\n        for {\n" +
		"            _ = Syscall(60, Trunc[uint](i64(code)), 0, 0, 0, 0, 0)\n        }\n    }\n}\n\n" + text[j:]
	file := nutshellFile(t)
	for _, c := range []struct {
		name    string
		std     map[string]string
		syscall bool
	}{
		{"placeholder", nil, false},
		{"backend", map[string]string{"sys/nutshell/sys.mk": backend}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MK_STD", realStdWith(t, c.std))
			exe := buildProgram(t, file, types.X86_64Nutshell, types.Dev, t.TempDir())
			code := elfSymbolBytes(t, exe, "mk.std.sys.Exit")
			if got := bytes.Contains(code, []byte{0x0f, 0x05}); got != c.syscall {
				t.Errorf("mk.std.sys.Exit holds syscall: %v, want %v (% x)", got, c.syscall, code)
			}
		})
	}
}

// elfSymbolBytes is the bytes of the defined function symbol name in the
// ELF executable exe.
func elfSymbolBytes(t *testing.T, exe, name string) []byte {
	t.Helper()
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Name != name || s.Section == elf.SHN_UNDEF || int(s.Section) >= len(f.Sections) {
			continue
		}
		sec := f.Sections[s.Section]
		data, err := sec.Data()
		if err != nil {
			t.Fatal(err)
		}
		off := s.Value - sec.Addr
		if s.Size == 0 || off+s.Size > uint64(len(data)) {
			t.Fatalf("%s: size %d at offset %#x of %s", name, s.Size, off, sec.Name)
		}
		return data[off : off+s.Size]
	}
	t.Fatalf("%s is not defined in %s", name, exe)
	return nil
}

// osMatchProgram matches Target.os with the four variants Os had before
// .Nutshell, then the arm OTHER, which osMatch replaces.
const osMatchProgram = `OsName(): str {
    match Target.os {
        .Linux {
            return "linux"
        }
        .Windows {
            return "windows"
        }
        .Macos {
            return "macos"
        }
        .Freestanding {
            return "freestanding"
        }
OTHER    }
}

export Main(sys: &System): WriteError!void {
    name := OsName()
    try sys.Print("{name}\n", (name))
}
`

// osMatch is osMatchProgram with a last arm of pattern last, or none when
// last is empty.
func osMatch(last string) string {
	arm := ""
	if last != "" {
		arm = "        " + last + " {\n            return \"other\"\n        }\n"
	}
	return strings.Replace(osMatchProgram, "OTHER", arm, 1)
}

// TestOsMatchKeepsDefaultArm: a match on Target.os that names the four
// variants other than .Nutshell is E0306 on every target, and the same
// match with a _ arm checks clean. With a .Nutshell arm instead it names
// every variant of Os, and since Os is open outside std it is E0306 reason
// open, which mk fix's `_` arm clears; the prelude's Os is named bare, as
// the program writes it.
func TestOsMatchKeepsDefaultArm(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		last, text string
		want       diag.Code
	}{{"", ".Nutshell", diag.E0306}, {"_", "", 0}, {".Nutshell", "may add to Os\n", diag.E0306}} {
		file := filepath.Join(t.TempDir(), "main.mk")
		if err := os.WriteFile(file, []byte(osMatch(c.last)), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, tg := range []types.Target{types.X86_64Linux, types.X86_64Nutshell} {
			r := check(t, Config{Root: file, Target: tg})
			ok := len(r.Report.Diags) == 0
			if c.want != 0 {
				ok = len(r.Report.Diags) == 1 && r.Report.Diags[0].Diag.Code == c.want &&
					strings.Contains(r.Report.Text(r.Sources), c.text)
			}
			if !ok {
				t.Errorf("last arm %q, mk check --target %s:\n%s", c.last, tg.Name, r.Report.Text(r.Sources))
			}
		}
	}
}
