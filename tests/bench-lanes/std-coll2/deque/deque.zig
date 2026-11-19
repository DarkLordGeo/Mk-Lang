// deque, Zig, std.Deque: the workload of README.md. No libc.
const std = @import("std");

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
    var d: std.Deque(u64) = .empty;
    defer d.deinit(a);
    var s = seed;
    var sum: u64 = 0;
    for (0..n) |i| {
        s +%= 0x9E3779B97F4A7C15;
        const x = mix(s);
        if (i & 15 == 15) {
            try d.pushFront(a, x);
            if (d.len > 1000) sum = sum *% 31 +% (d.popBack() orelse 0);
        } else {
            try d.pushBack(a, x);
            if (d.len > 1000) sum = sum *% 31 +% (d.popFront() orelse 0);
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d} {d}\n", .{ sum, d.len });
    try w.interface.flush();
}
