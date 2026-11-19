// verdict: reject E0306
// rule: D-718
// note: `fs.ReadBytes` fails with `fs.Error | OutOfMemory`, which holds every member of the open `fs.Error`, so it is open too: outside std a match with an arm per member of this release does not cover what `fs.Error` gains; `mk fix` adds the `_` arm (reason: open)
import std.fs

Size(sys: &System, a: &Arena): int {
    b := fs.ReadBytes(a, sys.io, "x") else e {
        return match e { // <- E0306
            fs.NotFound { 1 }
            fs.Exists { 2 }
            fs.Denied { 3 }
            fs.IsDirectory { 4 }
            fs.NotDirectory { 5 }
            fs.NoSpace { 6 }
            fs.InvalidPath { 7 }
            Utf8Error { 8 }
            fs.Os(code: _) { 9 }
            Canceled { 10 }
            fs.Unsupported { 11 }
            OutOfMemory { 12 }
        }
    }
    return b.Len()
}
