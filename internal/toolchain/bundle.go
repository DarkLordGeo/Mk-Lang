// Why: mk can ship with its own LLVM beside it; the bundle must be found
// from the real executable's path, and one lld must serve both linker
// flavours.
//
// What: linker and runLinker (the linker program and arguments of a link),
// RealPath, and InstallAdvice, E0001's advice when no usable LLVM is found.
//
// How: the named linker (lld-link, ld.lld) runs when the directory has it,
// else lld under `-flavor link` or `-flavor gnu`; RealPath resolves every
// symbolic link and junction so ../llvm/bin is looked up beside the real
// executable.

package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// linker is the program a link of the format runs, with the arguments that
// come before the link's own: the named linker (lld-link for COFF, ld.lld
// for ELF) when Dir has it, else lld under `-flavor link` or `-flavor gnu`.
// With neither, the named linker with an empty path, which
// run reports as not found.
func (t *Tools) linker(f Format) (tool, path string, pre []string) {
	tool, path, flavor := NameLLDLink, t.LLDLink, "link"
	if f == ELF {
		tool, path, flavor = NameLDLLD, t.LDLLD, "gnu"
	}
	if path == "" && t.LLD != "" {
		return NameLLD, t.LLD, []string{"-flavor", flavor}
	}
	return tool, path, nil
}

// runLinker runs the format's linker (linker) with args in dir. Every link
// goes through it: plain, ThinLTO index and final, PDB and stub links. A
// long command line goes through a response file (rsp.go).
func (t *Tools) runLinker(ctx context.Context, f Format, args []string, dir string) ([]byte, error) {
	return t.runLinkerIn(ctx, f, args, dir, false)
}

// RealPath is p made absolute with every symbolic link and junction among
// its components replaced by its target, so that the bundle rule finds
// ../llvm/bin beside the real executable. filepath.EvalSymlinks leaves a
// Windows junction (a mount point) unresolved, so each component is read
// with os.Readlink. A component that cannot be read stays as it is, and a
// chain longer than 64 links stops there.
func RealPath(p string) string {
	p = abs(p)
	for hops := 0; hops < 64; hops++ {
		vol := filepath.VolumeName(p)
		parts := strings.FieldsFunc(p[len(vol):], func(r rune) bool { return r == '/' || r == filepath.Separator })
		cur := vol + string(filepath.Separator)
		changed := false
		for i, part := range parts {
			next := filepath.Join(cur, part)
			fi, err := os.Lstat(next)
			if err != nil {
				break
			}
			if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				if target, err := os.Readlink(next); err == nil && target != "" {
					if !filepath.IsAbs(target) {
						target = filepath.Join(cur, target)
					}
					p = filepath.Join(append([]string{target}, parts[i+1:]...)...)
					changed = true
					break
				}
			}
			cur = next
		}
		if !changed {
			return p
		}
	}
	return p
}

// minText and maxText are MinMajor and MaxTestedMajor (two digits each) as
// constant text, so that InstallAdvice is a constant that names them rather
// than literal numbers (TestInstallAdvice holds it to fmt's rendering).
const (
	minText = string(rune('0'+MinMajor/10)) + string(rune('0'+MinMajor%10))
	maxText = string(rune('0'+MaxTestedMajor/10)) + string(rune('0'+MaxTestedMajor%10))
)

// InstallAdvice is E0001's first alternative when no usable LLVM was found:
// the supported range from MinMajor and MaxTestedMajor, and the bundle. It
// holds no path, host or version of this machine.
const InstallAdvice = "install LLVM " + minText + " or newer (tested on " + maxText + ") with clang and lld, " +
	"and put its bin directory on PATH, or install the Mk toolchain bundle, which carries its own LLVM"
