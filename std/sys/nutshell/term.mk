// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: the terminal-restore calls TermRestoreMode, TermRawAgain,
// TermContinueInstall, TermVt, TermVtOn and TermVtBack, and the record kinds.
//
// How: there is no terminal here; each call does nothing or answers its
// empty value.

/// TermKindTermios has std/sys/linux's contract.
export const TermKindTermios: u32 = 1
/// TermKindConsole has std/sys/linux's contract.
export const TermKindConsole: u32 = 2
/// TermKindTest has std/sys/linux's contract.
export const TermKindTest: u32 = 3

/// TermRestoreMode has std/sys/linux's contract; this placeholder has no
/// terminal and does nothing. O(1); allocates nothing.
export TermRestoreMode(kind: u32, h: uint, out: uint, mode: []u8, alt: bool) {
}

/// TermRawAgain has std/sys/linux's contract; this placeholder has
/// no terminal and does nothing. O(1); allocates nothing.
export TermRawAgain(kind: u32, h: uint) {
}

/// TermContinueInstall has std/sys/linux's contract; this
/// placeholder has no signal to install for. O(1); allocates nothing.
export TermContinueInstall(again: fn()) {
}

/// TermVt has std/sys/linux's contract; this placeholder has no
/// terminal. O(1); allocates nothing.
export TermVt(h: uint): bool {
    return false
}

/// TermVtOn has std/sys/linux's contract; this placeholder has no
/// terminal. O(1); allocates nothing.
export TermVtOn(kind: u32, out: uint): u64 {
    return 0
}

/// TermVtBack has std/sys/linux's contract; this placeholder has
/// no terminal. O(1); allocates nothing.
export TermVtBack(out: uint, held: u64) {
}
