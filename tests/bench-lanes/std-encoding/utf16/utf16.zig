// encoding-utf16, Zig, idiomatic std: the UTF-16LE form of the line of
// README.md repeated n times, decoded to UTF-8 rounds times with
// std.unicode.utf16LeToUtf8 into one buffer allocated before the loop
// (the units are an aligned []u16 made once; the input holds no unpaired
// surrogate, so Zig's error path is not reached). The checksum adds, per
// round, the text's length and one byte of it at a moving index.
// Usage: encoding-utf16 <n> <rounds>.
const std = @import("std");

const line = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n";

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const pa = std.heap.page_allocator;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    const text = try pa.alloc(u8, line.len * n);
    defer pa.free(text);
    for (0..n) |i| @memcpy(text[i * line.len .. (i + 1) * line.len], line);
    const units = try std.unicode.utf8ToUtf16LeAlloc(pa, text);
    defer pa.free(units);
    const out = try pa.alloc(u8, units.len * 3);
    defer pa.free(out);
    var total: u64 = 0;
    for (0..rounds) |r| {
        const m = try std.unicode.utf16LeToUtf8(out, units);
        total +%= m + out[(r * 7919) % m];
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
