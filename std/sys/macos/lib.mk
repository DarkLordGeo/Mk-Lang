// Why: a Mk library runs inside a host process; Mk libraries are built for
// x86_64-windows and x86_64-linux only, so on macOS this interface is never
// reached.
//
// What: LibraryImage, FaultUnhook, LibraryEnv and LibraryOwnerHook.
//
// How: Mk libraries are built for x86_64-windows and x86_64-linux only, so
// these are never reached; each answers its empty value.

import std.raw

/// LibraryImage has std/sys/linux's contract; on macOS it answers no
/// bounds. O(1); allocates nothing.
export LibraryImage(): (lo: uint, hi: uint) {
    return (lo: 0, hi: 0)
}

/// FaultUnhook has std/sys/linux's contract; on macOS it does nothing.
/// O(1); allocates nothing.
export FaultUnhook() {
}

/// LibraryEnv has std/sys/linux's contract; on macOS it has no
/// environment: null. O(1); allocates nothing.
export LibraryEnv(): *u8 {
    return raw.Null[u8]()
}

/// LibraryOwnerHook has std/sys/windows's contract; the macOS layer does
/// nothing. O(1); allocates nothing.
export LibraryOwnerHook(f: fn(uint): bool) {
}
