// encoding-utf8valid, Zig, idiomatic std: the line of README.md repeated n
// times, decoded rounds times into a buffer allocated and freed each round
// (a fresh allocation, as every column makes): std.unicode.utf8ValidateSlice,
// then a copy (the lossy std.unicode.fmtUtf8 only when the check fails,
// which it never does here). The checksum adds,
// per round, the text's length and one byte of it at a moving index.
// Usage: encoding-utf8valid <n> <rounds>.
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
    var total: u64 = 0;
    for (0..rounds) |r| {
        const out = try pa.alloc(u8, b.len * 3);
        defer pa.free(out);
        var m: usize = undefined;
        if (std.unicode.utf8ValidateSlice(b)) {
            @memcpy(out[0..b.len], b);
            m = b.len;
        } else {
            var w = std.Io.Writer.fixed(out);
            try w.print("{f}", .{std.unicode.fmtUtf8(b)});
            m = w.end;
        }
        total +%= m + out[(r * 7919) % m];
    }
    var obuf: [64]u8 = undefined;
    var sw = std.Io.File.stdout().writer(init.io, &obuf);
    try sw.interface.print("{d}\n", .{total});
    try sw.interface.flush();
}
