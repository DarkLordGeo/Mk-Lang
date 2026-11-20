// Why: Mk compares module-level names ignoring case and underscores, so
// libSystem's dlopen and dlclose cannot be declared in std.sys beside its
// own DlOpen and DlClose.
//
// What: those two externs, private here, and one wrapper each for std.sys
// (DynOpen, DynClose).
//
// How: each wrapper is the call and nothing more; the caller reads
// dlerror() for a cause, on the same thread, right after. Every name is in
// std/sys/macos/libSystem.def.

extern dlclose(h: *u8): i32
extern dlopen(path: *u8, mode: i32): *u8

/// DynOpen is dlopen(3) of the NUL-terminated path with mode's RTLD_ bits:
/// the handle, or null. Needs unsafe: path is the caller's. Runs the
/// library's initializers on the calling stack.
/// O(1) plus the loader's work; allocates what dyld allocates.
export DynOpen(path: *u8, mode: i32): *u8 {
    unsafe "path is a NUL-terminated string the caller holds" {
        return dlopen(path, mode)
    }
}

/// DynClose is dlclose(3) of a handle DynOpen gave: 0, or nonzero with
/// dlerror() set.
/// O(1) plus the loader's work; allocates nothing.
export DynClose(h: *u8): i32 {
    unsafe "h is a handle dlopen gave, which the caller owns" {
        return dlclose(h)
    }
}
