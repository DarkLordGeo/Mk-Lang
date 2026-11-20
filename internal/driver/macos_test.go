// Why: no Mac runs these executables here, so what can be checked of a
// macOS build is its file: the format, the entry, the one library it loads
// and the bytes of a second build.
// What: TestMacOSMachO.
// How: dev and release builds of a small program for aarch64-macos and
// x86_64-macos are read with debug/macho: an MH_EXECUTE image for the
// target's CPU, an LC_MAIN load command, LC_BUILD_VERSION, libSystem as
// its only library, every undefined symbol a name std's libSystem list
// holds, an LC_CODE_SIGNATURE on arm64; a second build from an empty
// cache gives the same bytes.

package driver

import (
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// Mach-O load commands debug/macho does not decode.
const (
	lcMain          = 0x80000028
	lcBuildVersion  = 0x32
	lcCodeSignature = 0x1d
)

func TestMacOSMachO(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "hi.mk")
	if err := os.WriteFile(file, []byte(t8Program), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "std", "sys", "macos", "libSystem.def"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := toolchain.ParseDefList(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{"dyld_stub_binder": true}
	for _, n := range names {
		listed["_"+n] = true
	}
	for _, tc := range []struct {
		target types.Target
		cpu    macho.Cpu
	}{{types.Aarch64Macos, macho.CpuArm64}, {types.X86_64Macos, macho.CpuAmd64}} {
		for _, prof := range []types.Profile{types.Dev, types.Release} {
			t.Run(tc.target.Name+"/"+prof.String(), func(t *testing.T) {
				exe := buildProgram(t, file, tc.target, prof, t.TempDir())
				f, err := macho.Open(exe)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if f.Cpu != tc.cpu || f.Type != macho.TypeExec {
					t.Errorf("cpu %v type %v, want %v MH_EXECUTE", f.Cpu, f.Type, tc.cpu)
				}
				cmds := map[uint32]bool{}
				for _, l := range f.Loads {
					b := l.Raw()
					if len(b) >= 4 {
						cmds[binary.LittleEndian.Uint32(b)] = true
					}
				}
				if !cmds[lcMain] || !cmds[lcBuildVersion] {
					t.Errorf("LC_MAIN %v, LC_BUILD_VERSION %v", cmds[lcMain], cmds[lcBuildVersion])
				}
				if sig := cmds[lcCodeSignature]; sig != (tc.target.Arch == types.ArchArm64) {
					t.Errorf("LC_CODE_SIGNATURE %v on %s", sig, tc.target.Name)
				}
				libs, err := f.ImportedLibraries()
				if err != nil || len(libs) != 1 || libs[0] != toolchain.LibSystemPath {
					t.Errorf("libraries %q (%v), want only %s", libs, err, toolchain.LibSystemPath)
				}
				syms, err := f.ImportedSymbols()
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range syms {
					if !listed[s] {
						t.Errorf("imports %s, which std/sys/macos/libSystem.def does not list", s)
					}
				}
				again := buildProgram(t, file, tc.target, prof, t.TempDir())
				a, err := os.ReadFile(exe)
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(again)
				if err != nil {
					t.Fatal(err)
				}
				if string(a) != string(b) {
					t.Errorf("two builds differ: %d and %d bytes", len(a), len(b))
				}
			})
		}
	}
}
