// Why: linking against the system libc on Linux must not let the loader bind
// a library's reference to the executable's own copy of a name.
// What: TestLibcModeArgs and TestLibcModeExportsNothing.
// How: checks the version script on the link line, then links a real
// executable and reads its .dynsym.

package toolchain

import (
	"context"
	"debug/elf"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLibcModeArgs: a system-libc link's line carries the executable's
// version script after --hash-style=gnu; an Interp link without an absolute
// one, and a version script on a static line, are job errors.
func TestLibcModeArgs(t *testing.T) {
	out, o1, libc, libm, ver := absPath(t, "a"), absPath(t, "1.o"), absPath(t, "so000.so"), absPath(t, "so001.so"), absPath(t, "exe.ver")
	j := LinkJob{Format: ELF, Output: out, Objects: []string{o1}, Entry: "mk_libc_start", Interp: "/lib64/ld-linux-x86-64.so.2",
		Shared: []string{libc, libm}, VersionScript: ver}
	got, err := linkArgs(j)
	want := []string{"-nostdlib", "-e", "mk_libc_start", "--build-id=none", "-z", "noexecstack",
		"--dynamic-linker=/lib64/ld-linux-x86-64.so.2", "-z", "now", "-z", "relro", "--hash-style=gnu", "--version-script=" + ver,
		"-o", out, o1, "--no-as-needed", libc, "--as-needed", libm}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("libc-mode line =\n%q, %v\nwant\n%q", got, err, want)
	}
	for _, bad := range []LinkJob{
		{Format: ELF, Output: out, Objects: []string{o1}, Interp: j.Interp, Shared: j.Shared},
		{Format: ELF, Output: out, Objects: []string{o1}, Interp: j.Interp, Shared: j.Shared, VersionScript: "exe.ver"},
		{Format: ELF, Output: out, Objects: []string{o1}, VersionScript: ver},
	} {
		if _, err := linkArgs(bad); err == nil {
			t.Errorf("linkArgs took %+v", bad)
		}
	}
}

// TestLibcModeExportsNothing: an executable that defines a name its stub
// also defines keeps it local in a system-libc link, so its .dynsym defines
// nothing and only imports: the loader can never bind a library's reference
// to the executable's copy. The reference to the stub's other name stays
// versioned.
func TestLibcModeExportsNothing(t *testing.T) {
	tl := realTools(t)
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	asm := func(name, text string) string {
		obj := filepath.Join(dir, name+".o")
		if err := tl.CompileAsm(context.Background(), CJob{Triple: "x86_64-unknown-linux-gnu", Input: write(name+".s", text), Output: obj}); err != nil {
			t.Fatal(err)
		}
		return obj
	}
	gnuStack := "\t.section .note.GNU-stack,\"\",@progbits\n"
	stubObj := asm("stub", "\t.text\n\t.globl malloc\n\t.type malloc,@function\nmalloc:\n\tret\n\t.globl close\n\t.type close,@function\nclose:\n\tret\n"+gnuStack)
	stub := filepath.Join(dir, "libc.so")
	if err := tl.LinkStub(context.Background(), stubObj, "libc.so.6", write("stub.ver", "GLIBC_2.2.5 { global: malloc; close; };\n"), stub); err != nil {
		t.Fatal(err)
	}
	exeObj := asm("exe", "\t.text\n\t.globl _start\n_start:\n\tcall malloc\n\tcall close@PLT\n\tret\n\t.globl malloc\n\t.type malloc,@function\nmalloc:\n\tret\n"+gnuStack)
	exe := filepath.Join(dir, "a.out")
	err := tl.Link(context.Background(), LinkJob{Format: ELF, Output: exe, Objects: []string{exeObj}, Entry: "_start",
		Interp: "/lib64/ld-linux-x86-64.so.2", Shared: []string{stub}, VersionScript: write("exe.ver", "{ local: *; };\n")})
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.DynamicSymbols()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
		if s.Section != elf.SHN_UNDEF {
			t.Errorf(".dynsym defines %s", s.Name)
		}
	}
	imp, _ := f.ImportedSymbols()
	if !reflect.DeepEqual(imp, []elf.ImportedSymbol{{Name: "close", Version: "GLIBC_2.2.5", Library: "libc.so.6"}}) {
		t.Errorf(".dynsym %q, imports %+v; want only close@GLIBC_2.2.5 of libc.so.6", names, imp)
	}
}
