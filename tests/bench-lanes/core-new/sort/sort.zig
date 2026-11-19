// sort, Zig, idiomatic std: the task of README.md with std.mem.sortUnstable
// (pdqsort) and std.sort.asc.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn fold(xs: []const u64) u64 {
    const n = xs.len;
    var sum = xs[n - 1];
    var i: usize = 0;
    while (i < n) : (i += 1024) sum +%= xs[i] *% @as(u64, i);
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    const xs = try std.heap.page_allocator.alloc(u64, n);
    defer std.heap.page_allocator.free(xs);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        var st = seed +% r *% 0x5555555555555555;
        for (xs) |*x| {
            st +%= 0x9e3779b97f4a7c15;
            x.* = mix(st);
        }
        std.mem.sortUnstable(u64, xs, {}, std.sort.asc(u64));
        total +%= fold(xs);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
