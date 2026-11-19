// search, Zig, idiomatic std: the task of README.md with std.sort.lowerBound
// (the first index whose element is not less than the key, through
// std.sort.partitionPoint) and a comptime comparator, std.math.order, so the
// call is direct. The program compares xs[i] with the key itself. Usage:
// search <n> <lookups> <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn order(key: u64, item: u64) std.math.Order {
    return std.math.order(key, item);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const lookups = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const xs = try pa.alloc(u64, n);
    defer pa.free(xs);
    for (xs, 0..) |*x, i| x.* = 2 * @as(u64, i) + 1;
    const span = 2 * @as(u64, n) + 1;
    var total: u64 = 0;
    var j: u64 = 0;
    while (j < lookups) : (j += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const k = mix(st) % span;
        const i = std.sort.lowerBound(u64, xs, k, order);
        if (i < n and xs[i] == k) {
            total +%= @as(u64, i) + 1;
        } else {
            total +%= 1;
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
