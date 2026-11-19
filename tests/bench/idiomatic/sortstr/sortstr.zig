// sortstr, Zig, idiomatic std: the task of README.md with std.sort.pdq
// (unstable) over []const u8 views into one buffer, ordered by
// std.mem.lessThan(u8, ...) (bytewise), a comptime comparator, so the call
// is direct. Usage: sortstr <n> <rounds> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn fnv(w: []const u8) u64 {
    var h: u64 = 0xcbf29ce484222325;
    for (w) |c| h = (h ^ c) *% 0x100000001b3;
    return h;
}

fn less(_: void, a: []const u8, b: []const u8) bool {
    return std.mem.lessThan(u8, a, b);
}

fn check(xs: []const []const u8) u64 {
    var sum: u64 = 0;
    for (xs, 0..) |x, i| {
        if (i > 0 and less({}, x, xs[i - 1])) return 0xdead;
        sum +%= fnv(x) *% @as(u64, i % 1000 + 1);
    }
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const buf = try pa.alloc(u8, n * 16);
    defer pa.free(buf);
    const orig = try pa.alloc([]const u8, n);
    defer pa.free(orig);
    const work = try pa.alloc([]const u8, n);
    defer pa.free(work);
    var p: usize = 0;
    for (orig) |*o| {
        const start = p;
        st +%= 0x9e3779b97f4a7c15;
        const l = 1 + @as(usize, @intCast(mix(st) % 16));
        for (0..l) |_| {
            st +%= 0x9e3779b97f4a7c15;
            buf[p] = @intCast(0x61 + mix(st) % 26);
            p += 1;
        }
        o.* = buf[start..p];
    }
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        @memcpy(work, orig);
        std.sort.pdq([]const u8, work, {}, less);
        total +%= check(work);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
