// compress-gunzip, Zig, idiomatic std, one-shot shape:
// std.compress.flate.Decompress in its gzip container (it checks the
// trailer's CRC-32 and length) in direct mode (an empty window buffer), so
// it decodes straight into the writer: a fixed std.Io.Writer over one
// output buffer of the trailer's ISIZE plus flate.max_window_len bytes,
// made once before the loop. Each round wraps the file in a fixed
// std.Io.Reader, streamRemaining decodes the member into the buffer, and
// every decompressed byte and the length are added to the checksum.
// Usage: compress-gunzip <file> <rounds>.
const std = @import("std");
const flate = std.compress.flate;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const gz = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(gz);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    if (gz.len < 18) return error.NotGzip;
    const size = std.mem.readInt(u32, gz[gz.len - 4 ..][0..4], .little);
    const outbuf = try pa.alloc(u8, @as(usize, size) + flate.max_window_len);
    defer pa.free(outbuf);
    var total: u64 = 0;
    for (0..rounds) |_| {
        var in: std.Io.Reader = .fixed(gz);
        var d: flate.Decompress = .init(&in, .gzip, &.{});
        var w: std.Io.Writer = .fixed(outbuf);
        const n = try d.reader.streamRemaining(&w);
        for (outbuf[0..n]) |c| total +%= c;
        total +%= n;
    }
    var out: [64]u8 = undefined;
    var sw = std.Io.File.stdout().writer(init.io, &out);
    try sw.interface.print("{d}\n", .{total});
    try sw.interface.flush();
}
