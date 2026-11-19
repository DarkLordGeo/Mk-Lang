// compress-gzip, Zig, idiomatic std: std.compress.flate.Compress in its gzip
// container at Options.default (zlib's level 6 parameters, Zig's default) of
// a 50 MB log text, rounds times. The file is read once; each round writes
// into a fixed std.Io.Writer over one output buffer (allocated once, larger
// than any gzip of the input), with a window buffer of flate.max_window_len
// bytes, one writeAll and finish. The checksum adds every input byte and
// the input length once per round; the compressed size goes to stderr.
// Usage: compress-gzip <file> <rounds>.
const std = @import("std");
const flate = std.compress.flate;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const text = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(text);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const window = try pa.alloc(u8, flate.max_window_len);
    defer pa.free(window);
    const outbuf = try pa.alloc(u8, text.len + (text.len >> 4) + (1 << 20));
    defer pa.free(outbuf);
    var total: u64 = 0;
    var size: usize = 0;
    for (0..rounds) |_| {
        var out: std.Io.Writer = .fixed(outbuf);
        var c = try flate.Compress.init(&out, window, .gzip, .default);
        try c.writer.writeAll(text);
        try c.finish();
        size = out.end;
        for (text) |b| total +%= b;
        total +%= text.len;
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
    std.debug.print("compressed {d}\n", .{size});
}
