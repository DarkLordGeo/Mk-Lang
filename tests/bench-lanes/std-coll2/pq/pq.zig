// pq, Zig, std.PriorityQueue (a min-heap by the compare function): the
// workload of README.md. No libc.
const std = @import("std");

fn less(_: void, a: u64, b: u64) std.math.Order {
    return std.math.order(a, b);
}

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 3) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const seed = try std.fmt.parseInt(u64, args[2], 10);
    const a = std.heap.page_allocator;
    var q = std.PriorityQueue(u64, void, less).initContext({});
    defer q.deinit(a);
    var s = seed;
    for (0..n) |_| {
        s +%= 0x9E3779B97F4A7C15;
        try q.push(a, mix(s));
    }
    var sum: u64 = 0;
    while (q.pop()) |x| sum = sum *% 31 +% x;
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{sum});
    try w.interface.flush();
}
