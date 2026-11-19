// verdict: ok
// case: R21.Save
import std.fs

Save(io: Io, data: []u8): fs.Error!void {
    var f := try fs.Create(io, "out.txt")
    onerr f.Abandon()
    try f.WriteAll(io, data)
    try f.Close(io)
}
