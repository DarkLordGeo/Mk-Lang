// Why: a bin that links a dynamic library for its Linux target must call libc
// for threads and the loader, while every other bin links no libc at all.
//
// What: std's libc table Libc, the slot indexes (Lc...) and LibcFn.
//
// How: such a bin is in system-libc mode: it enters through the driver's
// libc-mode entry (internal/codegen LibcEntryAsm),
// whose main writes the libc functions' addresses into Libc before mk_start
// runs and before any other thread exists, then never again. Elsewhere the
// table stays zero, std names no libc symbol and LibcBuild is false. Slot i
// is 8 * i bytes from the symbol mk.std.sys.Libc, in codegen.LibcSlotNames'
// order.

import std.raw

struct LibcWords {
    f: [10]raw.Atomic[u64]
}

var Libc: LibcWords = LibcWords(f: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
    raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
    raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)])

// The slots, in the order of codegen.LibcSlotNames.
const LcDlopen: int = 0
const LcDlsym: int = 1
const LcDlclose: int = 2
const LcDlerror: int = 3
const LcPthreadCreate: int = 4
const LcAttrInit: int = 5
const LcAttrSetStack: int = 6
const LcAttrSetDetach: int = 7
const LcAttrDestroy: int = 8
const LcErrno: int = 9

// Whether this process is in system-libc mode is LibcBuild (syscall.mk),
// a constant of the build: the driver links the libc-mode entry
// exactly when mono folds it to true, so the table is filled exactly then
// (pthread_create is always there).

// LibcFn is slot i's address, 0 outside system-libc mode.
LibcFn(i: int): u64 {
    // Edge: the entry's stores happen before mk_start, on the one thread
    // that then starts every other.
    return Libc.f[i].Load()
}
