// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: NotEmptyCode, NameAt, DirOpenAt and RemoveAt, the RemoveAll
// primitives.
//
// How: each answers Unsupported.

/// NotEmptyCode has std/sys/linux's value (ENOTEMPTY).
export const NotEmptyCode: i64 = 39

/// NameAt has std/sys/linux's contract. O(len); allocates nothing.
export NameAt(name: []u8, dir: bool): bool {
    if name.Len() == 0 {
        return false
    }
    if !dir {
        return true
    }
    if name.Len() == 1 && name[0] == 46 {
        return false
    }
    if name.Len() == 2 && name[0] == 46 && name[1] == 46 {
        return false
    }
    for x in name {
        if x == 47 || x == 0 {
            return false
        }
    }
    return true
}

/// DirOpenAt has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export DirOpenAt(dir: uint, held: []u8, name: []u8, fresh: mut []u8): OsResult {
    return NotHere()
}

/// RemoveAt has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export RemoveAt(dir: uint, held: []u8, name: []u8, is_dir: bool): OsResult {
    return NotHere()
}
