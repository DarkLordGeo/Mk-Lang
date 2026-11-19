// zlib-d, Zig, std: the task of README.md with std.compress.flate.Decompress
// in its zlib container (RFC 1950). The compressed file is read through a
// streaming File.Reader with a 64 KiB buffer, the decompressed stream is
// read in 64 KiB chunks with readSliceShort, and each chunk is written
// through a streaming File.Writer with a 64 KiB buffer. Zig 0.17's
// Decompress reads the zlib trailer but never compares it (Decompress.zig,
// .protocol_footer only stores adler), while zlib, flate2, Go and Mk check
// it, so this program checks it itself with std's own zlib hasher
// (flate.Container.Hasher, std.hash.Adler32, the hasher flate.Compress
// writes the trailer with) over every decompressed chunk, and fails with
// error.WrongZlibChecksum, as the task requires. Decompress checks
// only CM and CINFO of the header, so the program also checks FCHECK and
// refuses FDICT, as RFC 1950 2.3 requires and the other cells do, with
// error.BadZlibHeader. The program prints the count of decompressed bytes.
// Usage: zlibd <in> <out>.
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
    const buf = try pa.alloc(u8, 64 << 10);
    defer pa.free(buf);
    const window = try pa.alloc(u8, flate.max_window_len);
    defer pa.free(window);
    var fr = src.readerStreaming(io, rbuf);
    var fw = dst.writerStreaming(io, wbuf);
    // RFC 1950 2.3: a compliant decompressor checks CMF and FLG. Zig 0.17's
    // Decompress checks CM and CINFO only, so the program checks FCHECK
    // and refuses a preset dictionary (FDICT) itself; peekArray leaves the
    // two bytes for Decompress.
    const hdr = try fr.interface.peekArray(2);
    const cmf_flg = (@as(u16, hdr[0]) << 8) | hdr[1];
    if (cmf_flg % 31 != 0 or hdr[1] & 0x20 != 0) return error.BadZlibHeader;
    var d: flate.Decompress = .init(&fr.interface, .zlib, window);
    var h: flate.Container.Hasher = .init(.zlib);
    var total: u64 = 0;
    while (true) {
        const n = try d.reader.readSliceShort(buf);
        h.update(buf[0..n]);
        try fw.interface.writeAll(buf[0..n]);
        total += n;
        if (n < buf.len) break;
    }
    try fw.interface.flush();
    // The trailer as Decompress read it; it is filled once the stream has
    // ended, which the short read above reports.
    if (d.container_metadata.zlib.adler != h.zlib.adler) return error.WrongZlibChecksum;
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
