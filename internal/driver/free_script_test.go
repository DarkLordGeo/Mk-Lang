// Why: a kernel needs its own linker script and assembly; both must reach
// the link, and a change to either must rebuild the executable.
// What: TestLinkScriptAndAsmSources.
// How: a freestanding bin's link_script reaches ld.lld as -T and is part of
// the executable's key, and the package's asm_sources for the build target
// are assembled and linked while those for another target are not.

package driver

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

const scriptProgram = `extern AsmAnswer(): int

export extern MkEntry() {
    var x := 0
    unsafe "boot.s defines AsmAnswer, which returns 42" {
        x = AsmAnswer()
    }
    for {
        x += 0
    }
}
`

const scriptAsm = `    .text
    .globl AsmAnswer
AsmAnswer:
    movl $42, %eax
    ret
`

// otherAsm would collide with scriptAsm if it were linked.
const otherAsm = `    .text
    .globl AsmAnswer
AsmAnswer:
    ud2
`

func scriptLD(base string) string {
	return "ENTRY(MkEntry)\nSECTIONS\n{\n    . = " + base + ";\n    .text : { *(.text .text.*) }\n" +
		"    .rodata : { *(.rodata .rodata.*) }\n    .data : { *(.data .data.*) }\n    .bss : { *(.bss .bss.*) }\n" +
		"    ScriptMark = 0x1234;\n}\n"
}

// TestLinkScriptAndAsmSources: .text starts at the script's address,
// the script's symbol is defined, AsmAnswer comes from the freestanding
// asm source only, the link job carries the script, and editing the script
// relinks a warm build to the new address.
func TestLinkScriptAndAsmSources(t *testing.T) {
	t.Parallel()
	tools := needTools(t)
	dir := writeTree(t, map[string]string{
		"main.mk":   scriptProgram,
		"boot.s":    scriptAsm,
		"linux.s":   otherAsm,
		"kernel.ld": scriptLD("0x200000"),
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "p", root: ".", link_script: "kernel.ld", cpu: "", subsystem: "console")]`,
			"asm_sources":    `[pkg.AsmSource(path: "boot.s", target: "x86_64-freestanding"), pkg.AsmSource(path: "linux.s", target: "x86_64-linux")]`,
			"unsafe_modules": `["."]`}),
	})
	cache := t.TempDir()
	build := func(name string) (string, []string) {
		var scripts []string
		cfg := Config{Root: dir, Target: types.X86_64Freestanding, Tools: tools, CacheDir: cache}
		cfg.TestHooks.OnLink = func(j toolchain.LinkJob) { scripts = append(scripts, j.Script) }
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		r, paths, err := s.Build(filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Report.Diags) > 0 || len(paths) != 1 {
			t.Fatalf("build %s:\n%s", name, r.Report.Text(r.Sources))
		}
		return paths[0], scripts
	}
	look := func(path string, base uint64) {
		t.Helper()
		f, err := elf.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if s := f.Section(".text"); s == nil || s.Addr != base {
			t.Errorf(".text %v, want it at %#x", s, base)
		}
		syms, err := f.Symbols()
		if err != nil {
			t.Fatal(err)
		}
		var mark, answer int
		for _, s := range syms {
			switch s.Name {
			case "ScriptMark":
				mark++
				if s.Value != 0x1234 {
					t.Errorf("ScriptMark %#x, want 0x1234", s.Value)
				}
			case "AsmAnswer":
				answer++
			}
		}
		if mark != 1 || answer != 1 {
			t.Errorf("ScriptMark defined %d times, AsmAnswer %d times; want 1 and 1", mark, answer)
		}
	}
	exe, scripts := build("cold")
	if len(scripts) != 1 || scripts[0] != filepath.Join(dir, "kernel.ld") {
		t.Errorf("link scripts %q, want [%s]", scripts, filepath.Join(dir, "kernel.ld"))
	}
	look(exe, 0x200000)
	if err := os.WriteFile(filepath.Join(dir, "kernel.ld"), []byte(scriptLD("0x400000")), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, _ = build("edited")
	look(exe, 0x400000)
}
