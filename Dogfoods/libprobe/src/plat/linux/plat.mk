// Why: the per-target `plat` module for Linux, so the Linux build names no
// Windows function.
// What: Name, SystemDir and FileVersion, with NotHere and NoVersion.
// How: Linux keeps no PE files and no version resources, so each answer
// says so.

/// Why the system cannot answer.
export error NotHere

/// The directory that holds the system's own PE libraries, or none on a
/// system that has none.
export SystemDir(sys: &System): ?str {
    _ = sys
    return none
}

/// The file version of `path` as four numbers, major first, read from its
/// version resource. Fails with `NotHere` on Linux.
export FileVersion(a: &Arena, path: str): NotHere|NoVersion|OutOfMemory![4]int {
    _ = a
    _ = path
    fail NotHere
}

/// The file holds no version resource, or it could not be read.
export error NoVersion

/// The name of this system, for messages.
export const Name: str = "linux"
