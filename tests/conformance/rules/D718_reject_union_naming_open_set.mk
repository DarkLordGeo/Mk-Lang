// verdict: reject E0306
// rule: D-718
// note: a set that holds every member of an open alias is open: `fs.Error | Mine` grows when `fs.Error` does, so outside std a match with an arm per member of this release does not cover it; `mk fix` adds the `_` arm (reason: open)
import std.fs

error Mine

type E = fs.Error | Mine

Code(e: E): int {
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
        Mine { 12 }
    }
}
