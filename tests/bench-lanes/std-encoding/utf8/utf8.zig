// encoding-utf8, Zig, idiomatic std: the line of README.md repeated n
// times, with the first ASCII byte at or after every 997th offset replaced
// by 0xFF, decoded rounds times by std.unicode.fmtUtf8 (the WHATWG decoder,
// maximal subparts) printed into a fixed Writer over a buffer allocated and
// freed each round (a fresh allocation, as every column makes). The
// checksum adds, per round, the text's length and one byte of it at a
// moving index.
// Usage: encoding-utf8 <n> <rounds>.
const std = @import("std");

const line = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n";

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const b = try pa.alloc(u8, line.len * n);
    defer pa.free(b);
    for (0..n) |i| @memcpy(b[i * line.len .. (i + 1) * line.len], line);
    var p: usize = 996;
    while (p < b.len) : (p += 997) {
        var q = p;
        while (q < b.len and b[q] >= 0x80) q += 1;
        if (q < b.len) b[q] = 0xFF;
    }
    var total: u64 = 0;
    for (0..rounds) |r| {
        const out = try pa.alloc(u8, b.len * 3);
        defer pa.free(out);
        var w = std.Io.Writer.fixed(out);
        try w.print("{f}", .{std.unicode.fmtUtf8(b)});
        const m = w.end;
        total +%= m + out[(r * 7919) % m];
    }
    var obuf: [64]u8 = undefined;
    var sw = std.Io.File.stdout().writer(init.io, &obuf);
    try sw.interface.print("{d}\n", .{total});
    try sw.interface.flush();
}
