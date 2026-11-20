// Why: a macOS executable imports every system function from libSystem,
// and the build machine has no macOS SDK whose libSystem.tbd the link
// could read.
//
// What: macosStub, the text of the libSystem stub the link reads and its
// key, and libSystemList, std's name list in the std tree.
//
// How: std/sys/macos/libSystem.def lists every libSystem function std
// calls, one per line, as kernel32.def does for Windows; the stub
// (toolchain.TBD) exports those names for both macOS architectures. The
// stub's key hashes its text and the Mach-O linker's path and size, which
// Tools' identity does not cover, so a change to either relinks.

package driver

import (
	"os"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/toolchain"
)

// libSystemList is std's list of libSystem names, in the std tree.
const libSystemList = "sys/macos/libSystem.def"

// macosStub is the libSystem stub a macOS link reads, with its link key.
func (w *world) macosStub(tools *toolchain.Tools) (string, source.Hash, *diag.Diagnostic) {
	raw, err := w.stdTree().readFile(libSystemList)
	if err != nil {
		return "", source.Hash{}, diag.Toolchain("fs", "read std/"+libSystemList, err.Error(), "cannot read std/"+libSystemList)
	}
	names, err := toolchain.ParseDefList(string(raw))
	if err != nil {
		return "", source.Hash{}, diag.Toolchain("ld64.lld", "", err.Error(), "std/"+libSystemList+" is not a name list")
	}
	text := toolchain.TBD(names)
	h := source.NewHasher("mk.driver.libsystem.v1")
	h.String(text)
	ld := tools.LD64()
	h.String(ld)
	var size int64 = -1
	if fi, err := os.Stat(ld); err == nil {
		size = fi.Size()
	}
	h.Varint(size)
	return text, h.Sum(), nil
}
