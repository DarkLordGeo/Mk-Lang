// Why: an output file must appear whole or not at all, even across a crash.
//
// What: Stage and Place, and TempPrefix.
//
// How: the file is built under a temporary name beside its final path, made
// by fs.CreateTemp, synced, then renamed into place; a crash leaves at most
// a temporary file named `.pack-` and 16 hex digits.

import std.fs

/// The prefix of the temporary files pack makes.
const TempPrefix: str = ".pack-"

/// The directory `out` will be in, as `fs.CreateTemp` takes it.
Dir(out: str): str from out {
    var i := out.Len()
    for i > 0 {
        i -= 1
        c := out.Byte(i)
        if c == b'/' || c == b'\\' {
            return if i == 0 { out[..1] } else { out[..i] }
        }
    }
    return "."
}

/// Makes the temporary file `out` is built in, open for writing, and
/// gives it with its path. Fails with `Failed` once it has reported why.
/// The caller writes it, syncs and closes it with `onerr Discard` in
/// force, then calls `Place`.
Stage(c: &Ctx, a: &Arena, out: str): Failed|OutOfMemory!(file: fs.File, temp: str) from a {
    (file, path) := fs.CreateTemp(a, c.io, Dir(out), TempPrefix) else e {
        failure: Failed | OutOfMemory = match e {
            OutOfMemory { OutOfMemory }
            other { c.Fail(out, other) }
        }
        fail failure
    }
    return (file: file, temp: path)
}

/// Renames the finished temporary file `temp`, synced and closed, over
/// `out`, and removes it when that fails.
Place(c: &Ctx, temp: str, out: str): Failed!void {
    fs.Rename(c.io, temp, out) else e {
        fs.Remove(c.io, temp) else {}
        fail c.Fail(out, e)
    }
}
