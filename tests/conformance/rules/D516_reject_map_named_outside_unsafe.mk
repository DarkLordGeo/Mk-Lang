// verdict: reject E1101
// rule: D-516
// note: naming fs.Map as a value outside unsafe is E1101 at the name, since a call through the fn value is a call the call rule never sees
import std.fs

Mapper(): fn(Io, &fs.File, i64, int): fs.Error!fs.Mapping {
    return fs.Map // <- E1101
}
