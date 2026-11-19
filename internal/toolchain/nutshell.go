// Why: Nutshell executables are loaded by the Nutshell component loader,
// which maps a static ELF at a fixed address and applies nothing after
// mapping.
//
// What: IsNutshellTriple (x86_64-nutshell, aarch64-nutshell) and
// NutshellLinkFlags.
//
// How: the link gives ET_EXEC, no PT_INTERP and no dynamic section, no
// segment both writable and executable, every PT_LOAD page-aligned in the
// file and in memory, and no RELRO.

package toolchain

import "strings"

// NutshellLinkFlags follow the hosted ELF line (-static -nostdlib -e
// _start --build-id=none -z noexecstack) in a Nutshell link: --no-pie, so
// the image is ET_EXEC at ld.lld's default base whatever the objects are;
// -z norelro, since the loader maps each segment once with its final
// protection; and -z separate-loadable-segments, so every PT_LOAD starts on
// a page boundary in the file and in memory and maps as whole pages.
var NutshellLinkFlags = []string{"--no-pie", "-z", "norelro", "-z", "separate-loadable-segments"}

// IsNutshellTriple reports whether triple names the Nutshell OS
// (x86_64-unknown-nutshell-elf, aarch64-unknown-nutshell-elf): ELF
// executables that no host runs.
func IsNutshellTriple(triple string) bool {
	parts := strings.Split(triple, "-")
	for _, p := range parts[1:] {
		if p == "nutshell" {
			return true
		}
	}
	return false
}
