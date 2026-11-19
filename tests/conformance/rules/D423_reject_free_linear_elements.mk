// verdict: reject E0701
// rule: D-423
// note: Free of a container whose element type is linear and not Free is refused at the call (FreeEmpty or FreeEach instead)
import std.fs

Leak(a: &Alloc, files: HeapList[fs.File]) {
    files.Free(a) // <- E0701
}

LeakRes(a: &Alloc, m: HeapMap[int, Res]) {
    m.Free(a) // <- E0701
}
