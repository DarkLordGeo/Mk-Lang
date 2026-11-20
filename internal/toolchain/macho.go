// Why: macOS executables are Mach-O, linked by ld64.lld against libSystem,
// which every macOS program loads; no SDK is on the build machine, so the
// link must not need one.
//
// What: NameLD64LLD, MacOSMin, MachOEntry, MachOArch, machoArgs (the link
// line), Tools.ld64 (the linker's path) and TBD, the text stub of
// libSystem a link reads in place of Apple's.
//
// How: ld64.lld links against a text-based stub (TAPI version 4) exactly as
// against Apple's own .tbd files: the stub names the install path
// /usr/lib/libSystem.B.dylib and the symbols the program may import, and
// dyld binds them to the real libSystem at load. ld64.lld signs arm64
// output ad hoc, which arm64 macOS requires of every executable, and
// derives LC_UUID from the output's bytes, so the link is deterministic.

package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// NameLD64LLD is lld's Mach-O linker.
const NameLD64LLD = "ld64.lld"

// MacOSMin is the minimum macOS release the macOS targets run on and the
// SDK version the executable records: 11.0, the first with Apple silicon.
const MacOSMin = "11.0"

// MachOEntry is the entry symbol of a macOS executable, main as the
// assembler spells it; dyld calls it through LC_MAIN.
const MachOEntry = "_main"

// LibSystemPath is libSystem's install name, which the stub records and
// the executable's LC_LOAD_DYLIB names.
const LibSystemPath = "/usr/lib/libSystem.B.dylib"

// MachOArch is the ld64 architecture name of a triple, "arm64" or
// "x86_64", "" for a triple of any other architecture.
func MachOArch(triple string) string {
	switch ArchOf(triple) {
	case "arm64":
		return "arm64"
	case "amd64":
		return "x86_64"
	}
	return ""
}

// ld64 is the path of ld64.lld in Dir, "" when Dir has none (linker then
// runs lld -flavor darwin).
func (t *Tools) ld64() string {
	for _, name := range []string{NameLD64LLD + ".exe", NameLD64LLD} {
		p := filepath.Join(t.Dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// LD64 is the Mach-O linker a link runs: ld64.lld's path, or lld's under
// -flavor darwin. Its path and size enter a macOS link's key, since Tools'
// identity does not cover it.
func (t *Tools) LD64() string {
	_, p, _ := t.linker(MachO)
	return p
}

// machoArgs is a Mach-O link's command line after the linker:
//
//	-arch A -platform_version macos 11.0 11.0 -e E -o BASE(OUT) OBJS ARCHIVES
//	LIBS -S [-dead_strip [-x]] [--lto-O3 [--thinlto-jobs=J] -mllvm ...]
//
// LIBS holds the libSystem stub (TBD). A release link drops unreferenced
// code and data (-dead_strip) and, without Debug, its local symbols (-x),
// as an ELF one passes --strip-all. No link keeps a debug map, so
// --debug gives a macOS executable its symbols but no source-level
// debugging (that would need dsymutil's .dSYM, which mk does not make).
func machoArgs(j LinkJob) ([]string, error) {
	if j.MachOArch != "arm64" && j.MachOArch != "x86_64" {
		return nil, errors.New("toolchain: a Mach-O link needs MachOArch arm64 or x86_64")
	}
	if j.Free || j.Nutshell || j.Script != "" || j.Interp != "" || len(j.Shared) > 0 || j.VersionScript != "" ||
		j.DependentLoadFlags != 0 || j.SectionAlign != 0 || j.StackReserve != 0 {
		return nil, errors.New("toolchain: a Mach-O link takes no freestanding, Nutshell, system-libc or COFF option")
	}
	if len(j.Libs) == 0 {
		return nil, errors.New("toolchain: a Mach-O link needs libSystem's stub in Libs")
	}
	entry := j.Entry
	if entry == "" {
		entry = MachOEntry
	}
	// -o names the output by its base name, in the directory the link runs
	// in (Link runs every linker in the output's directory): ld64.lld
	// takes the code signature's identifier from that argument, and a whole
	// path there would make the bytes depend on where the build ran.
	args := []string{"-arch", j.MachOArch, "-platform_version", "macos", MacOSMin, MacOSMin, "-e", entry, "-o", filepath.Base(j.Output)}
	args = append(args, j.Objects...)
	args = append(args, j.Archives...)
	args = append(args, j.Libs...)
	// -S drops the debug map (N_OSO stabs), which names each object by
	// its path: Mach-O leaves DWARF in the objects, which the build does not
	// keep, so the map would only make the bytes depend on where the build
	// ran.
	args = append(args, "-S")
	if j.Release {
		args = append(args, "-dead_strip")
		if !j.Debug {
			args = append(args, "-x")
		}
	}
	if j.LTO {
		args = append(args, "--lto-O3")
		if j.Jobs > 0 && !j.Tune.FatLTO {
			args = append(args, "--thinlto-jobs="+strconv.Itoa(j.Jobs))
		}
		args = append(args, j.Tune.mllvm()...)
	}
	return args, nil
}

// TBD is the text of a TAPI version 4 stub for libSystem exporting names
// (C names, without Mach-O's '_') for both macOS architectures, plus
// dyld_stub_binder, which ld64.lld's lazy bindings call. Names are sorted
// and deduplicated, so the stub's bytes depend on the set alone.
func TBD(names []string) string {
	set := map[string]bool{"dyld_stub_binder": true}
	for _, n := range names {
		if n != "dyld_stub_binder" {
			n = "_" + n
		}
		set[n] = true
	}
	var syms []string
	for n := range set {
		syms = append(syms, n)
	}
	sort.Strings(syms)
	var b strings.Builder
	b.WriteString("--- !tapi-tbd\ntbd-version:     4\ntargets:         [ x86_64-macos, arm64-macos ]\n")
	b.WriteString("install-name:    '" + LibSystemPath + "'\ncurrent-version: 1351\n")
	b.WriteString("exports:\n  - targets:     [ x86_64-macos, arm64-macos ]\n    symbols:     [ ")
	for i, s := range syms {
		if i > 0 {
			b.WriteString(",\n                   ")
		}
		b.WriteString(s)
	}
	b.WriteString(" ]\n...\n")
	return b.String()
}
