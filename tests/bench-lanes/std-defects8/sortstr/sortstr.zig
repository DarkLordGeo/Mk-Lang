// sortstr, Zig, idiomatic std: the task of README.md with
// std.mem.sortUnstable (pdqsort) over a slice of 16-byte slices of one
// buffer, compared by std.mem.lessThan.
const std = @import("std");

const hex = "0123456789abcdef";

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn unhex(s: []const u8) u64 {
    var v: u64 = 0;
    for (s) |c| {
        const d: u64 = if (c <= '9') c - '0' else c - 'a' + 10;
        v = (v << 4) | d;
    }
    return v;
}

fn fold(xs: []const []const u8) u64 {
    const n = xs.len;
    var sum = unhex(xs[n - 1]);
    var i: usize = 0;
    while (i < n) : (i += 1024) sum +%= unhex(xs[i]) *% @as(u64, i);
    return sum;
}

fn less(_: void, a: []const u8, b: []const u8) bool {
    return std.mem.lessThan(u8, a, b);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    const buf = try std.heap.page_allocator.alloc(u8, n * 16);
    defer std.heap.page_allocator.free(buf);
    const xs = try std.heap.page_allocator.alloc([]const u8, n);
    defer std.heap.page_allocator.free(xs);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        var st = seed +% r *% 0x5555555555555555;
        for (xs, 0..) |*x, i| {
            st +%= 0x9e3779b97f4a7c15;
            const z = mix(st);
            const k = buf[i * 16 ..][0..16];
            for (k, 0..) |*c, j| {
                const sh: u6 = @intCast(60 - 4 * j);
                c.* = hex[@intCast((z >> sh) & 15)];
            }
            x.* = k;
        }
        std.mem.sortUnstable([]const u8, xs, {}, less);
        total +%= fold(xs);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
