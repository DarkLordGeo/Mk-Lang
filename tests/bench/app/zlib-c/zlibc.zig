// zlib-c, Zig, std: the task of README.md with std.compress.flate.Compress
// in its zlib container (RFC 1950) at Options.level_6, zlib's level 6
// parameters (Compress.zig takes them from zlib 1.3.1's deflate.c). The
// input file is read through a streaming File.Reader with a 64 KiB buffer
// and streamed into the compressor's writer, which writes through a
// streaming File.Writer with a 64 KiB buffer; finish writes the final block
// and the Adler-32 trailer. The program prints the count of input bytes.
// Usage: zlibc <in> <out>.
const std = @import("std");
const flate = std.compress.flate;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const io = init.io;
    const pa = std.heap.page_allocator;
    const cwd = std.Io.Dir.cwd();
    const src = try cwd.openFile(io, args[1], .{});
    defer src.close(io);
    const dst = try cwd.createFile(io, args[2], .{});
    defer dst.close(io);
    const rbuf = try pa.alloc(u8, 64 << 10);
    defer pa.free(rbuf);
    const wbuf = try pa.alloc(u8, 64 << 10);
    defer pa.free(wbuf);
    const window = try pa.alloc(u8, flate.max_window_len);
    defer pa.free(window);
    var fr = src.readerStreaming(io, rbuf);
    var fw = dst.writerStreaming(io, wbuf);
    var c = try flate.Compress.init(&fw.interface, window, .zlib, .level_6);
    const total = try fr.interface.streamRemaining(&c.writer);
    try c.finish();
    try fw.interface.flush();
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
