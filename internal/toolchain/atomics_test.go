// Why: clang's driver turns aarch64 outline atomics on when the build host
// has a recent GCC, and then an atomic calls a libgcc helper mk never
// links, so an aarch64 build must not depend on the host.
// What: TestAArch64NoOutlineAtomics.
// How: a module with a compare-and-swap, an atomic add and a swap is
// compiled by mk's own command line for aarch64-linux and aarch64-macos,
// with -moutline-atomics put before it to stand for the host's default; the
// object must have no undefined __aarch64_ symbol. The same compile without
// mk's flag must have one, so the test shows the default is overridden.

package toolchain

import (
	"context"
	"debug/elf"
	"debug/macho"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const atomicsIR = `define i32 @f(ptr %p, i32 %a, i32 %b) {
  %c = cmpxchg ptr %p, i32 %a, i32 %b acq_rel monotonic
  %v = extractvalue { i32, i1 } %c, 0
  %s = atomicrmw add ptr %p, i32 %v acq_rel
  %w = atomicrmw xchg ptr %p, i32 %s acq_rel
  ret i32 %w
}
`

// undefinedAArch64 lists the object's undefined symbols named __aarch64_*
// (_ __aarch64_* on Mach-O).
func undefinedAArch64(t *testing.T, obj string, macOS bool) []string {
	t.Helper()
	var out []string
	if macOS {
		f, err := macho.Open(obj)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if f.Symtab != nil {
			for _, s := range f.Symtab.Syms {
				if s.Type&0x0e == 0 && strings.HasPrefix(s.Name, "___aarch64_") {
					out = append(out, s.Name)
				}
			}
		}
		return out
	}
	f, err := elf.Open(obj)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range syms {
		if s.Section == elf.SHN_UNDEF && strings.HasPrefix(s.Name, "__aarch64_") {
			out = append(out, s.Name)
		}
	}
	return out
}

func TestAArch64NoOutlineAtomics(t *testing.T) {
	tl := realTools(t)
	for _, triple := range []string{"aarch64-unknown-linux-gnu", "arm64-apple-macosx11.0.0"} {
		t.Run(triple, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "a.ll")
			if err := os.WriteFile(in, []byte(atomicsIR), 0o644); err != nil {
				t.Fatal(err)
			}
			macOS := strings.Contains(triple, "apple")
			for _, tc := range []struct {
				name string
				drop bool // drop mk's -mno-outline-atomics: the control
			}{{"mk", false}, {"host default", true}} {
				out := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".o")
				args, err := compileArgs(CompileJob{Triple: triple, Input: in, Output: out, Opt: O3, CPU: "generic"})
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(args, "-mno-outline-atomics") {
					t.Fatalf("compileArgs for %s has no -mno-outline-atomics: %q", triple, args)
				}
				if tc.drop {
					args = slices.DeleteFunc(args, func(a string) bool { return a == "-mno-outline-atomics" })
				}
				// The host's default, first, as clang's driver would set it.
				args = append([]string{args[0], "-moutline-atomics"}, args[1:]...)
				if _, err := run(context.Background(), NameClang, tl.Clang, args, dir); err != nil {
					t.Fatal(err)
				}
				got := undefinedAArch64(t, out, macOS)
				switch {
				case !tc.drop && len(got) > 0:
					t.Errorf("mk's compile calls outline-atomics helpers: %q", got)
				case tc.drop && len(got) == 0:
					t.Errorf("the control (outline atomics on) calls no helper: the test proves nothing")
				}
			}
		})
	}
}
