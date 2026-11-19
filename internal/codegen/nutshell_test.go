// Why: x86_64-nutshell is a Linux-like target with its own entry, so its
// support pack shares Linux's routines but not its process start.
// What: TestNutshellSupportGolden and TestNutshellSupportPack:
// Linux's System V switch, task start and system-stack call; Nutshell's
// _start, which pushes the two start words and passes their address to
// mk_start; .size on every routine; no mk_clone or mk_sigreturn.
// How: the pack is compared with its golden, and clang compiles it for
// x86_64-unknown-nutshell-elf to an object with exactly those symbols.

package codegen

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestNutshellSupportGolden(t *testing.T) {
	target := types.X86_64Nutshell
	got := supportLL(t, target)
	if again := supportLL(t, target); again != got {
		t.Fatal("two emissions of the support pack differ")
	}
	path := filepath.Join("testdata", "support."+target.Name+".ll.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Errorf("support pack for %s differs from %s (run with -update and read the diff)", target.Name, path)
	}
}

func TestNutshellSupportPack(t *testing.T) {
	ll := supportLL(t, types.X86_64Nutshell)
	if !strings.Contains(ll, `target triple = "x86_64-unknown-nutshell-elf"`) {
		t.Errorf("triple: %s", firstLine(ll))
	}
	start := []string{".cfi_startproc", ".cfi_undefined %rip", "pushq %rsi", "pushq %rdi", "xorl %ebp, %ebp", "movq %rsp, %rdi", "andq $-16, %rsp", "callq mk_start", "ud2", ".cfi_endproc"}
	if body := asmBody(ll, symELFStart); !equalLines(body, start) {
		t.Errorf("_start is %q, want %q", body, start)
	}
	linux := supportLL(t, types.X86_64Linux)
	for _, name := range []string{symSwitch, symTaskStart, symCallSystem} {
		if body := asmBody(ll, name); len(body) == 0 || !equalLines(body, asmBody(linux, name)) {
			t.Errorf("%s is not x86_64-linux's System V routine:\n%s", name, strings.Join(body, "\n"))
		}
	}
	for _, name := range []string{symELFStart, symSwitch, symTaskStart, symCallSystem} {
		if !strings.Contains(ll, `.size `+name+`, .-`+name) {
			t.Errorf("no .size for %s", name)
		}
	}
	for _, bad := range []string{symClone, symSigreturn, symChkstk, "noredzone"} {
		if strings.Contains(ll, bad) {
			t.Errorf("the Nutshell support pack names %q", bad)
		}
	}
	if !strings.Contains(ll, "@mk_start(ptr") || !strings.Contains(ll, `"probe-stack"="inline-asm"`) {
		t.Errorf("no mk_start(sp) definition with inline stack probes")
	}
	obj := clangC(t, types.X86_64Nutshell, t.TempDir(), "support", ll, "-O2")
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 || f.Class != elf.ELFCLASS64 || f.Type != elf.ET_REL {
		t.Errorf("object: machine %v class %v type %v", f.Machine, f.Class, f.Type)
	}
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, s := range syms {
		if s.Section != elf.SHN_UNDEF {
			defined[s.Name] = true
		}
	}
	for _, n := range []string{symELFStart, symStart, symSwitch, symTaskStart, symCallSystem} {
		if !defined[n] {
			t.Errorf("%s is not defined in the object", n)
		}
	}
	for _, n := range []string{symClone, symSigreturn} {
		if defined[n] {
			t.Errorf("%s is defined in the object", n)
		}
	}
}

// asmBody is the instructions of the module-assembly routine name: the
// lines after its label up to its .size, trimmed.
func asmBody(ll, name string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(ll, "\n") {
		rest, ok := strings.CutPrefix(line, "module asm \"")
		if !ok {
			continue
		}
		rest = strings.TrimSuffix(rest, "\"")
		rest = strings.TrimSpace(strings.ReplaceAll(rest, `\09`, ""))
		switch {
		case rest == name+":":
			in = true
		case in && strings.HasPrefix(rest, ".size"):
			return out
		case in:
			out = append(out, rest)
		}
	}
	return out
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
