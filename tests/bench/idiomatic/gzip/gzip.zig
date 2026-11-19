// gzip, Zig, idiomatic std: the task of README.md with
// std.compress.flate.Decompress in its gzip container. The file is read
// once; each round wraps it in a fixed std.Io.Reader, makes a Decompress
// with a window buffer of flate.max_window_len bytes, and reads the
// decompressed stream in 64 KiB chunks with readSliceShort, adding every
// byte and the length to the checksum. Zig 0.17's Decompress reads the
// gzip trailer but never compares it (Decompress.zig, .protocol_footer only
// stores crc and count), while Go's gzip.Reader and Mk's Inflater check it,
// so this program checks it itself with std's own gzip hasher
// (flate.Container.Hasher: std.hash.Crc32 and the length, the hasher
// flate.Compress writes the trailer with) over every decompressed chunk,
// and fails with error.WrongGzipChecksum or error.WrongGzipSize, as the
// task requires. Usage: gzip <file> <rounds>.
const std = @import("std");
const flate = std.compress.flate;

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const gz = try std.Io.Dir.cwd().readFileAlloc(init.io, args[1], pa, .unlimited);
    defer pa.free(gz);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const window = try pa.alloc(u8, flate.max_window_len);
    defer pa.free(window);
    const buf = try pa.alloc(u8, 64 << 10);
    defer pa.free(buf);
    var total: u64 = 0;
    for (0..rounds) |_| {
        var in: std.Io.Reader = .fixed(gz);
        var d: flate.Decompress = .init(&in, .gzip, window);
        var h: flate.Container.Hasher = .init(.gzip);
        while (true) {
            const n = try d.reader.readSliceShort(buf);
            h.update(buf[0..n]);
            for (buf[0..n]) |c| total +%= c;
            total +%= n;
            if (n < buf.len) break;
        }
        // The trailer as Decompress read it; it is filled once the stream
        // has ended, which the short read above reports.
        const trailer = d.container_metadata.gzip;
        if (trailer.crc != h.gzip.crc.final()) return error.WrongGzipChecksum;
        if (trailer.count != h.gzip.count) return error.WrongGzipSize;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
