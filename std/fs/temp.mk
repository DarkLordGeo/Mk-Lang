// Why: temporary files must get fresh names that no other process can take
// first.
//
// What: CreateTemp and TempDir.
//
// How: as Go's os.CreateTemp and os.TempDir: a name made of the prefix and
// random bytes from Io.Random, opened with O_CREAT|O_EXCL (CREATE_NEW),
// mode 0o600, tried again on Exists up to 10,000 times. Under a simulated
// Io both take the Linux form on every target, so a seed gives one run.

import std.core
import std.rt

// TempTries bounds CreateTemp's names (Go's os.CreateTemp: 10,000).
const TempTries = 10000

// TempHex is the random part's length: 8 bytes of Io.Random in hex.
const TempHex = 16

// SimBits are a simulated Io's token and slot bits in its flags: such an
// Io reads no variable of the real process.
const SimBits: uint = 0xFFFF_0000_FFFF_FFFE

// PathWin is whether paths take the Windows form under io: on Windows
// with a native Io. Under a simulated Io (SimBits) they take the Linux
// form on every target, `/` the only separator, so a seed gives one run.
PathWin(io: Io): bool {
    return Target.os == core.Os.Windows && (core.IsNative(io) || (core.IoFlags(io) & SimBits) == 0)
}

/// Creates a new file in dir and returns it, open for reading and writing,
/// with its path in a. The name is prefix followed by 16 random
/// hexadecimal digits, and the mode is 0o600. An empty dir means TempDir.
///
/// InvalidPath when prefix holds a separator (`/`, and `\` on Windows or
/// under a simulated Io). Exists when 10,000 names in a row are taken.
/// NotFound when dir is missing. Canceled when Io.Random is. Any other
/// failure is the open's, as its fs.Error member. OutOfMemory when a
/// cannot hold the path. The path is allocated before any file is made, so
/// a failure leaves no file behind.
///
/// Usually one open, at most 10,000, each O(1) runtime calls; allocates the
/// path in a, and TempDir's path when dir is empty.
///
/// Under a simulated Io (a Sim's or a Tape's) the name is joined to dir
/// with `/` on every target, so a seed gives one run, and a prefix with
/// `\`, which std.sim reads as `/`, is InvalidPath on every target. A
/// Recorder over the native Io counts as simulated here: on Windows it
/// joins with `/`, so a `\\?\` dir, which takes no `/`, is InvalidPath;
/// on Linux it refuses a `\` that the native Io takes as a name byte.
export CreateTemp(a: &Arena, io: Io, dir: str, prefix: str): Error|OutOfMemory!(file: File, path: str) from a {
    win := PathWin(io)
    // The prefix refuses both separators wherever one reads `\` as `/`:
    // Windows, and a simulated Io's tree on every target.
    for c in prefix.AsBytes() {
        if IsSep(c, Target.os == core.Os.Windows || (!core.IsNative(io) && (core.IoFlags(io) & SimBits) != 0)) {
            fail InvalidPath
        }
    }
    d := if dir.Len() > 0 { dir } else { try TempDir(a, io) }
    db := d.AsBytes()
    sep := if db.Len() > 0 && !IsSep(db[db.Len() - 1], win) { 1 } else { 0 }
    b := try a.Slice[u8](db.Len() + sep + prefix.Len() + TempHex)
    Put(b, 0, db)
    if sep == 1 {
        b[db.Len()] = if win { 0x5C } else { 0x2F }
    }
    at := db.Len() + sep + prefix.Len()
    Put(b, db.Len() + sep, prefix.AsBytes())
    var o := CreateNew
    o.permissions = Permissions(mode: 0o600)
    for try_ in 0..TempTries {
        _ = try_
        var r: [8]u8 = [0; 8]
        io.Random(mut r[..]) else {
            fail Canceled
        }
        for i in 0..8 {
            b[at + 2 * i] = HexDigit(r[i] >> 4)
            b[at + 2 * i + 1] = HexDigit(r[i] & 15)
        }
        // b is d, prefix and ASCII digits, all UTF-8.
        p := rt.StrFrom(b[..]) else {
            fail Utf8Error
        }
        f := OpenWith(io, p, o) else e {
            match e {
                Exists {
                    continue
                }
                _ {
                    fail e
                }
            }
        }
        return (file: f, path: p)
    }
    fail Exists
}

// HexDigit is the lowercase hexadecimal digit of v, 0 to 15.
HexDigit(v: u8): u8 {
    return if v < 10 { 0x30 + v } else { 0x57 + v }
}

/// The directory for temporary files, in a. On Linux it is TMPDIR, else
/// /tmp. On Windows it is the first of TMP, TEMP, USERPROFILE and
/// SystemRoot that is set (GetTempPath2W's order), without a trailing
/// separator unless that separator follows the drive. The directory is
/// not checked to exist.
///
/// NotFound on Windows when none of the variables is set; OutOfMemory when
/// a cannot hold the path. O(environment) for the lookups; allocates the
/// path in a.
///
/// Under a simulated Io (a Sim's or a Tape's) no variable is read and the
/// answer is /tmp on every target, so a seed gives one run. A Recorder
/// over the native Io on Windows answers /tmp too, which the real file
/// system reads as `\tmp` on the working drive.
export TempDir(a: &Arena, io: Io): Error|OutOfMemory!str from a {
    if (!core.IsNative(io) && (core.IoFlags(io) & SimBits) != 0) {
        return "/tmp"
    }
    if Target.os != core.Os.Windows {
        t := try EnvIn(a, "TMPDIR")
        return if t.Len() > 0 { t } else { "/tmp" }
    }
    for name in ["TMP", "TEMP", "USERPROFILE", "SystemRoot"] {
        v := try EnvIn(a, name)
        if v.Len() > 0 {
            b := v.AsBytes()
            var n := b.Len()
            if n > 1 && IsSep(b[n - 1], true) && b[n - 2] != 0x3A {
                n -= 1
            }
            return v[0..n]
        }
    }
    fail NotFound
}

// EnvIn is the process's variable name copied into a, or "" when unset.
EnvIn(a: &Arena, name: str): OutOfMemory!str from a {
    sys := core.NewSystem(rt.Args(), rt.NativeIo(), a, rt.ProcessHeap(), rt.Proc())
    v := sys.Env(name) else {
        return ""
    }
    return try a.Dup(v)
}
