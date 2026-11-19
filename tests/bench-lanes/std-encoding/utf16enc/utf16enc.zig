// encoding-utf16enc, Zig, idiomatic std: the line of README.md repeated n
// times, encoded to UTF-16LE rounds times with std.unicode.utf8ToUtf16Le
// into one []u16 allocated before the loop (byte i of the little-endian
// form is read from unit i / 2). The checksum adds, per round, the
// encoding's length in bytes and one byte of it at a moving index.
// Usage: encoding-utf16enc <n> <rounds>.
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
    const out = try pa.alloc(u16, text.len);
    defer pa.free(out);
    var total: u64 = 0;
    for (0..rounds) |r| {
        const units = try std.unicode.utf8ToUtf16Le(out, text);
        const m = 2 * units;
        const k = (r * 7919) % m;
        const byte: u8 = @truncate(std.mem.littleToNative(u16, out[k / 2]) >> @intCast(8 * (k & 1)));
        total +%= m + byte;
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
