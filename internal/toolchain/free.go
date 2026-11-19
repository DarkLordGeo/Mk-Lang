// Why: a freestanding target (x86_64-freestanding, for kernels and boot
// code) has no OS, no C runtime and no loader, so its link and assembly
// differ from every hosted one.
//
// What: FreeEntry, GeneralRegsOnly, LinkFree, AssembleFree and
// freeLinkArgs.
//
// How: a freestanding executable is a static ELF linked by ld.lld with no C
// runtime, no interpreter, no dynamic section and, when the bin names one,
// the package's linker script; asm_sources go through clang's integrated
// assembler.

package toolchain

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// FreeTriple is the LLVM triple of x86_64-freestanding (types.X86_64Freestanding).
const FreeTriple = "x86_64-unknown-none-elf"

// FreeEntry is the entry symbol of a freestanding executable: the function
// the bin exports as `export extern MkEntry`.
const FreeEntry = "MkEntry"

// GeneralRegsOnly is the "target-features" string clang 21.1.8 derives from
// -mgeneral-regs-only for x86_64-unknown-none-elf
// (TestGeneralRegsOnlyString in internal/codegen): no x87, MMX or SSE.
const GeneralRegsOnly = "-x87,-mmx,-sse"

// isFreeTriple reports whether triple names no OS (x86_64-unknown-none-elf,
// or any "-none" triple), whose executables are freestanding ELF.
func isFreeTriple(triple string) bool {
	parts := strings.Split(triple, "-")
	for _, p := range parts[1:] {
		if p == "none" {
			return true
		}
	}
	return false
}

// LinkFree links a freestanding ELF: ld.lld -static -nostdlib --no-pie -z
// norelro --build-id=none -z noexecstack -e entry [-T script] -o out objs....
// entry "" is FreeEntry; script "" means lld's default layout. Every path is
// absolute.
func LinkFree(ctx context.Context, t *Tools, objs []string, script, entry, out string) error {
	return t.Link(ctx, LinkJob{Format: ELF, Free: true, Script: script, Entry: entry, Output: out, Objects: objs})
}

// AssembleFree assembles one asm_sources file (.s, or .S through the
// preprocessor) for x86_64-freestanding with clang's integrated assembler:
// clang --target=x86_64-unknown-none-elf -x assembler -c.
func AssembleFree(ctx context.Context, t *Tools, src, out string) error {
	return t.CompileAsm(ctx, CJob{Triple: FreeTriple, Input: src, Output: out})
}

// freeLinkArgs is the ld.lld command line of a freestanding link
// (LinkJob.Free): the ELF line of linkArgs with --no-pie and -z norelro,
// the entry MkEntry by default, and -T <script> before the objects. Import
// libraries are refused; release adds --gc-sections and ThinLTO as for a
// hosted ELF link.
func freeLinkArgs(j LinkJob, policy string) ([]string, error) {
	if len(j.Libs) > 0 {
		return nil, errors.New("toolchain: import libraries are COFF only")
	}
	if j.Script != "" && !filepath.IsAbs(j.Script) {
		return nil, fmt.Errorf("toolchain: linker script must be absolute: %q", j.Script)
	}
	entry := j.Entry
	if entry == "" {
		entry = FreeEntry
	}
	args := []string{"-static", "-nostdlib", "--no-pie", "-z", "norelro", "--build-id=none", "-z", "noexecstack",
		"-e", entry, "-o", j.Output}
	if j.Script != "" {
		args = append(args, "-T", j.Script)
	}
	args = append(args, j.Objects...)
	if j.LTO {
		args = append(args, j.Tune.mllvm()...)
	}
	if j.Release {
		args = append(args, "--gc-sections")
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
