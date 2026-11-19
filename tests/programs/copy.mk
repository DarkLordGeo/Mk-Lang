// File copier with a fallible close: copies
// args[1] to args[2] through a Writer over a stack buffer, syncs and closes both files,
// with onerr Abandon on every error path. Then it reads the copy back and prints it, so
// the copy is checked through standard output.

import std.fs

Copy(io: Io, src: str, dst: str): fs.Error|WriteError!void {
    var input := try fs.Open(io, src)
    onerr input.Abandon()
    var output := try fs.Create(io, dst)
    onerr output.Abandon()
    var buf: [4096]u8 = [0; 4096]
    var wbuf: [512]u8 = [0; 512]
    var w := output.Writer(io, mut wbuf[..])
    for {
        n := try input.Read(io, mut buf[..])
        if n == 0 {
            break
        }
        try w.Bytes(buf[..n])
    }
    try w.Flush()
    try output.Sync(io)
    try output.Close(io)
    try input.Close(io)
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    try Copy(sys.io, sys.args[1], sys.args[2])
    back := try fs.ReadBytes(sys.arena, sys.io, sys.args[2])
    var out := sys.Stdout()
    try out.Bytes(back)
}
