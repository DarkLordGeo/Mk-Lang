// text-utf16, Zig: std.unicode.utf8ToUtf16LeAlloc (encode) and
// utf16LeToUtf8Alloc of one unit buffer (decode), std.heap.smp_allocator,
// each result freed each pass, over the input and passes of
// mk/text-utf16.mk's comment (README.md). Zig's encoder also validates its
// UTF-8 input (Mk's str is valid by type), and its decoder fails on an
// unpaired surrogate. Usage: text-utf16 <encode|decode> <mib> <reps> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn gen(a: std.mem.Allocator, n: usize, seed: u64) ![]u8 {
    var s: std.ArrayList(u8) = .empty;
    var st = seed;
    while (s.items.len < n) {
        st +%= 0x9e3779b97f4a7c15;
        const x = mix(st);
        const k = x % 16;
        const y = x >> 8;
        const c: u21 = if (k < 10)
            (if (x % 7 == 0) 0x41 + @as(u21, @intCast(y % 26)) else 0x61 + @as(u21, @intCast(y % 26)))
        else if (k == 10)
            0x20
        else if (k == 11)
            0xC0 + @as(u21, @intCast(y % 64))
        else if (k == 12)
            0x430 + @as(u21, @intCast(y % 32))
        else if (k == 13)
            0x4E00 + @as(u21, @intCast(y % 0x5000))
        else if (k == 14)
            0x1F600 + @as(u21, @intCast(y % 80))
        else
            0x300 + @as(u21, @intCast(y % 0x70));
        var buf: [4]u8 = undefined;
        const len = try std.unicode.utf8Encode(c, &buf);
        try s.appendSlice(a, buf[0..len]);
    }
    return s.toOwnedSlice(a);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 5) return error.Usage;
    const mode = args[1];
    const mib = try std.fmt.parseInt(usize, args[2], 10);
    const reps = try std.fmt.parseInt(usize, args[3], 10);
    const seed = try std.fmt.parseInt(u64, args[4], 10);
    const a = std.heap.smp_allocator;
    const s = try gen(a, mib * 1048576, seed);
    defer a.free(s);
    var sum: u64 = 0;
    if (std.mem.eql(u8, mode, "encode")) {
        for (0..reps) |r| {
            const u = try std.unicode.utf8ToUtf16LeAlloc(a, s);
            sum +%= @as(u64, u.len) +% @as(u64, u[r % u.len]);
            a.free(u);
        }
    } else if (std.mem.eql(u8, mode, "decode")) {
        const u = try std.unicode.utf8ToUtf16LeAlloc(a, s);
        defer a.free(u);
        for (0..reps) |r| {
            const back = try std.unicode.utf16LeToUtf8Alloc(a, u);
            sum +%= @as(u64, back.len) +% @as(u64, back[r % back.len]);
            a.free(back);
        }
    } else return error.Usage;
    var wbuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &wbuf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
