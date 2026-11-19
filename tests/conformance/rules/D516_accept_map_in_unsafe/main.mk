// verdict: ok
// rule: D-516
// note: pkg.mk permits unsafe in snap only, where fs.Map is called and named inside an unsafe block with its reason; len 0 maps nothing with no system call; Bytes is a view of the mapping, and Unmap consumes it
import std.fs
import snap

// Total opens path and sums its first 64 bytes through the mapping.
Total(io: Io, path: str): fs.Error!u64 {
    f := try fs.Open(io, path)
    onerr f.Abandon()
    n := try snap.Sum(io, &f, 64)
    try f.Close(io)
    return n
}
