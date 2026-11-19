// Why: std/sys/nutshell, the placeholder std.sys of the Nutshell targets,
// must offer whatever std.rt and std.task call on Linux, and make no system call.
// What: TestNutshellSysSurface and TestNutshellSyscallOnly.
// How: sysSurface compares the exports with std/sys/linux's, signature by
// signature and struct by struct; the second test wants only the Syscall intrinsic.
//
// Nutshell declares Linux's private Syscall intrinsic and no other (no Clone,
// no Restorer: its support pack has no mk_clone or mk_sigreturn), and no other
// file of it names Syscall.

package std

import (
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

func TestNutshellSysSurface(t *testing.T) {
	linux, nut := sysSurface(t, "sys/linux"), sysSurface(t, "sys/nutshell")
	if len(linux) == 0 {
		t.Fatal("std/sys/linux exports nothing (the reader no longer matches?)")
	}
	for _, e := range linux {
		if !slices.Contains(nut, e) {
			t.Errorf("std/sys/nutshell lacks Linux's export:\n%s", e)
		}
	}
	for _, e := range nut {
		if !slices.Contains(linux, e) {
			t.Errorf("std/sys/nutshell exports what Linux does not:\n%s", e)
		}
	}
}

func TestNutshellSyscallOnly(t *testing.T) {
	const decl = "Syscall(n: uint, a0: uint, a1: uint, a2: uint, a3: uint, a4: uint, a5: uint): int"
	for _, dir := range []string{"sys/linux", "sys/nutshell"} {
		if !slices.Contains(strings.Split(readText(t, dir+"/syscall.mk"), "\n"), decl) {
			t.Errorf("std/%s/syscall.mk does not declare %s", dir, decl)
		}
	}
	entries, err := os.ReadDir("sys/nutshell")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		for i, line := range strings.Split(readText(t, path.Join("sys/nutshell", e.Name())), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			switch {
			case e.Name() == "syscall.mk" && code != "" && code != decl:
				t.Errorf("std/sys/nutshell/syscall.mk:%d: %q; it declares Syscall alone", i+1, code)
			case e.Name() != "syscall.mk" && strings.Contains(strings.ReplaceAll(code, "export RawSyscall(", ""), "Syscall("):
				t.Errorf("std/sys/nutshell/%s:%d calls Syscall; the placeholder makes no system call", e.Name(), i+1)
			case strings.Contains(code, "extern"):
				t.Errorf("std/sys/nutshell/%s:%d declares an extern", e.Name(), i+1)
			}
		}
	}
}

// sysSurface is the exported API of a std.sys directory, one entry per
// top-level export: its declaration with a multi-line signature joined,
// the body's opening brace and a const's value dropped, and an exported
// struct's whole declaration, comment lines dropped.
func sysSurface(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".mk") {
			continue
		}
		lines := strings.Split(readText(t, path.Join(dir, e.Name())), "\n")
		for i := 0; i < len(lines); i++ {
			line := strings.TrimRight(lines[i], " \t")
			if !strings.HasPrefix(line, "export ") {
				continue
			}
			if strings.HasPrefix(line, "export struct ") {
				block := []string{line}
				for i+1 < len(lines) && lines[i] != "}" {
					i++
					if l := strings.TrimRight(lines[i], " \t"); !strings.HasPrefix(strings.TrimSpace(l), "//") {
						block = append(block, l)
					}
				}
				out = append(out, strings.Join(block, "\n"))
				continue
			}
			for strings.Count(line, "(") > strings.Count(line, ")") && i+1 < len(lines) {
				i++
				line += " " + strings.TrimSpace(lines[i])
			}
			if strings.HasPrefix(line, "export const ") {
				line, _, _ = strings.Cut(line, " = ")
			}
			out = append(out, strings.TrimSuffix(strings.TrimSuffix(line, "{"), " "))
		}
	}
	slices.Sort(out)
	return out
}
