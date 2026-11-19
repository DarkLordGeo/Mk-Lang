// compress-unzstd, Zig, idiomatic std: std.compress.zstd.Decompress (the
// default 8 MiB window) reads a zstd frame (libzstd 1.5.7 level 19 with no
// content checksum, 524,288 bytes of text) rounds times; the frame has no
// checksum, so no cell checks one (Zig 0.17 panics with "TODO all those
// bytes written needed to go through the hasher too" when verify_checksum
// is set). Each round opens the file and reads it through
// std.Io.File.Reader with a 64 KiB buffer, as the Mk column does; the
// window buffer (window_len + block_size_max) is made once. The output is
// read in 64 KiB chunks with readSliceShort; the checksum adds every
// decoded byte and each round's length.
// Usage: compress-unzstd <file> <rounds>.
const std = @import("std");
const zstd = std.compress.zstd;

fn round(io: std.Io, p: []const u8, rbuf: []u8, window: []u8, out: []u8) !u64 {
    const f = try std.Io.Dir.cwd().openFile(io, p, .{});
    defer f.close(io);
    var fr = f.reader(io, rbuf);
    var d: zstd.Decompress = .init(&fr.interface, window, .{});
    var sum: u64 = 0;
    var n: u64 = 0;
    while (true) {
        const k = try d.reader.readSliceShort(out);
        for (out[0..k]) |c| sum +%= c;
        n +%= k;
        if (k < out.len) break;
    }
    return sum +% n;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const window = try pa.alloc(u8, zstd.default_window_len + zstd.block_size_max);
    defer pa.free(window);
    const rbuf = try pa.alloc(u8, 64 << 10);
    defer pa.free(rbuf);
    const out = try pa.alloc(u8, 64 << 10);
    defer pa.free(out);
    var total: u64 = 0;
    for (0..rounds) |_| total +%= try round(init.io, args[1], rbuf, window, out);
    var ob: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &ob);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
