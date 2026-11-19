// compress-gzip-stream, Zig, idiomatic std: std.compress.flate.Compress in
// its gzip container at the default level, over a std.Io.File.Writer with
// a 64 KiB buffer to the null device (the path argument), takes a 50 MB
// log text in 64 KiB writes and is finished (the final block and the
// trailer), rounds times; its window buffer is made once. The checksum
// adds every input byte and the input length once per round.
// Usage: compress-gzip-stream <file> <out> <rounds>.
const std = @import("std");
const flate = std.compress.flate;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const io = init.io;
    const pa = std.heap.page_allocator;
    const text = try std.Io.Dir.cwd().readFileAlloc(io, args[1], pa, .unlimited);
    defer pa.free(text);
    const rounds = try std.fmt.parseInt(usize, args[3], 10);
    var sum: u64 = 0;
    for (text) |c| sum +%= c;
    sum +%= text.len;
    const f = try std.Io.Dir.cwd().createFile(io, args[2], .{});
    defer f.close(io);
    const wbuf = try pa.alloc(u8, 64 << 10);
    defer pa.free(wbuf);
    const window = try pa.alloc(u8, flate.max_window_len);
    defer pa.free(window);
    var fw = f.writer(io, wbuf);
    var total: u64 = 0;
    for (0..rounds) |_| {
        var c = try flate.Compress.init(&fw.interface, window, .gzip, .default);
        var p: usize = 0;
        while (p < text.len) : (p += 64 << 10) {
            try c.writer.writeAll(text[p..@min(p + (64 << 10), text.len)]);
        }
        try c.finish();
        total +%= sum;
    }
    try fw.interface.flush();
    var ob: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &ob);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
