// Why: a package can be built as a dynamic or static library for other
// programs to link, which needs other link lines than an executable.
//
// What: OutputKind, dynamicArgs (the COFF and ELF command lines of a
// Dynamic link) and StaticLib.
//
// How: a COFF DLL has no entry point and its runtime starts on the first
// call; an ELF shared object links with -Bsymbolic and a version script
// that keeps every symbol but the exports local. A static library is
// llvm-lib's .lib on COFF and a GNU archive written in Go (WriteArchive) on
// ELF.

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OutputKind is what a link or an archive step makes (pkg.Bin.kind).
type OutputKind uint8

// The output kinds. There is no object-file output kind.
const (
	Executable OutputKind = iota
	Dynamic
	Static
)

func (k OutputKind) String() string {
	switch k {
	case Dynamic:
		return "dynamic"
	case Static:
		return "static"
	}
	return "executable"
}

// dynamicArgs is a Dynamic link's command line after the linker.
//
// COFF: /nologo /nodefaultlib /dll /noentry /machine:x64 /Brepro /out:OUT
// /implib:IMP /def:DEF [/dependentloadflag:F] OBJS ARCHIVES LIBS, then the
// executable line's optimization, alignment, LTO and debug options. The
// library has no entry point: its runtime starts on the first call,
// and the loader's DllMain call finds none to run.
//
// ELF: -shared -nostdlib --build-id=none -z noexecstack -z now -z relro
// -z text --hash-style=gnu -Bsymbolic -soname=S --version-script=V -o OUT
// OBJS ARCHIVES, then the executable line's --gc-sections, --strip-all and
// LTO options. -Bsymbolic binds every reference inside the library to its
// own definition, so the position-independent code clang writes for the
// target (a PIE model) links with no text relocation; the version script
// keeps every symbol but the exports local.
func dynamicArgs(j LinkJob, policy string) ([]string, error) {
	if j.Free || j.Nutshell || j.Interp != "" || j.Script != "" || j.Entry != "" || j.StackReserve != 0 {
		return nil, errors.New("toolchain: a dynamic library is a hosted link with no entry, interpreter, script or stack size")
	}
	if j.Subsystem != "" && j.Subsystem != "console" {
		return nil, errors.New("toolchain: a dynamic library takes no subsystem")
	}
	switch j.Format {
	case COFF:
		if j.Soname != "" || j.VersionScript != "" {
			return nil, errors.New("toolchain: a soname and a version script are ELF only")
		}
		for _, p := range []string{j.ImpLib, j.Def} {
			if !filepath.IsAbs(p) {
				return nil, fmt.Errorf("toolchain: a DLL link needs an absolute import library and .def: %q", p)
			}
		}
		args := []string{"/nologo", "/nodefaultlib", "/dll", "/noentry", "/machine:x64", "/Brepro",
			"/out:" + j.Output, "/implib:" + j.ImpLib, "/def:" + j.Def}
		if j.DependentLoadFlags != 0 {
			args = append(args, fmt.Sprintf("/dependentloadflag:0x%X", j.DependentLoadFlags))
		}
		args = append(args, j.Objects...)
		args = append(args, j.Archives...)
		args = append(args, j.Libs...)
		if j.LTO {
			for _, o := range j.Tune.ltoOptions() {
				args = append(args, "/mllvm:"+o)
			}
		}
		if j.Release {
			args = append(args, "/opt:ref", "/opt:icf")
		} else {
			args = append(args, "/opt:noicf")
		}
		if j.SectionAlign > 0 {
			args = append(args, "/align:"+strconv.FormatUint(uint64(j.SectionAlign), 10))
		}
		if j.LTO {
			args = append(args, "/opt:lldlto=3")
			if j.LTOCacheDir != "" && j.Tune.IsZero() {
				args = append(args, "/lldltocache:"+j.LTOCacheDir, "/lldltocachepolicy:"+policy)
			}
			if j.Jobs > 0 && !j.Tune.FatLTO {
				args = append(args, "/opt:lldltojobs="+strconv.Itoa(j.Jobs))
			}
		}
		if j.Debug {
			args = append(args, "/debug:full", "/pdbaltpath:%_PDB%")
		}
		return args, nil
	case ELF:
		if j.ImpLib != "" || j.Def != "" || len(j.Libs) > 0 || j.DependentLoadFlags != 0 || j.SectionAlign != 0 {
			return nil, errors.New("toolchain: import libraries, .def files, load flags and section alignment are COFF only")
		}
		if j.Soname == "" || filepath.Base(j.Soname) != j.Soname {
			return nil, fmt.Errorf("toolchain: bad soname %q", j.Soname)
		}
		if !filepath.IsAbs(j.VersionScript) {
			return nil, fmt.Errorf("toolchain: a shared library needs an absolute version script: %q", j.VersionScript)
		}
		args := []string{"-shared", "-nostdlib", "--build-id=none", "-z", "noexecstack", "-z", "now", "-z", "relro",
			"-z", "text", "--hash-style=gnu", "-Bsymbolic", "-soname=" + j.Soname, "--version-script=" + j.VersionScript,
			"-o", j.Output}
		args = append(args, j.Objects...)
		args = append(args, j.Archives...)
		if j.LTO {
			args = append(args, j.Tune.mllvm()...)
		}
		if j.Release {
			args = append(args, "--gc-sections")
			if !j.Debug {
				args = append(args, "--strip-all")
			}
		}
		if j.LTO {
			args = append(args, "--lto-O3")
			if j.LTOCacheDir != "" && j.Tune.IsZero() {
				args = append(args, "--thinlto-cache-dir="+j.LTOCacheDir, "--thinlto-cache-policy="+policy)
			}
			if j.Jobs > 0 && !j.Tune.FatLTO {
				args = append(args, "--thinlto-jobs="+strconv.Itoa(j.Jobs))
			}
		}
		return args, nil
	}
	return nil, fmt.Errorf("toolchain: unknown link format %d", j.Format)
}

// StaticLib writes the static library out from objs, native objects in
// link order: on COFF a .lib written by llvm-lib (or lld-link
// /lib), through a response file when the list is long; on ELF a GNU
// archive with its symbol index, written in Go (WriteArchive), so the
// Linux bundle needs no archiver. Bitcode objects are refused: a host's
// own linker could not read them.
func (t *Tools) StaticLib(ctx context.Context, f Format, objs []string, out string) error {
	if !filepath.IsAbs(out) {
		return fmt.Errorf("toolchain: library path must be absolute: %q", out)
	}
	if len(objs) == 0 {
		return errors.New("toolchain: a static library with no objects")
	}
	for _, o := range objs {
		if !filepath.IsAbs(o) {
			return fmt.Errorf("toolchain: library input must be absolute: %q", o)
		}
		if bc, err := isBitcode(o); err != nil {
			return err
		} else if bc {
			return fmt.Errorf("toolchain: %s is bitcode; a static library holds native objects", o)
		}
	}
	switch f {
	case COFF:
		// llvm-lib records each member by the name it was given, so the
		// inputs are staged under fixed names (o0000.o, l0000.lib) in a
		// private directory and named relatively there, as pdbLink does:
		// the archive does not depend on where the build ran.
		work, err := os.MkdirTemp(filepath.Dir(out), "mklib-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		args := []string{"/nologo", "/machine:x64", "/out:out.lib"}
		for i, o := range objs {
			name := fmt.Sprintf("o%04d.o", i)
			if strings.EqualFold(filepath.Ext(o), ".lib") {
				name = fmt.Sprintf("l%04d.lib", i)
			}
			if err := linkOrCopy(o, filepath.Join(work, name)); err != nil {
				return err
			}
			args = append(args, name)
		}
		tool, path, all := t.importLibTool(args)
		if len(CommandLine(path, all)) < responseFileAt {
			_, err = run(ctx, tool, path, all, work)
		} else {
			pre := all[:len(all)-len(args)]
			if err := os.WriteFile(filepath.Join(work, "lib.rsp"), []byte(rspText(COFF, args)), 0o644); err != nil {
				return err
			}
			_, err = run(ctx, tool, path, append(append([]string(nil), pre...), "@lib.rsp"), work)
			var te *ToolError
			if errors.As(err, &te) {
				te.Command = CommandLine(path, all)
			}
		}
		if err != nil {
			return err
		}
		return os.Rename(filepath.Join(work, "out.lib"), out)
	case ELF:
		return WriteArchive(out, objs)
	}
	return fmt.Errorf("toolchain: unknown library format %d", f)
}
