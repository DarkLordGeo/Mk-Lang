// sort, Zig, idiomatic std: the task of README.md with std.sort.pdq
// (pattern-defeating quicksort, unstable) and std.sort.asc(u64), a
// comptime comparator, so the call is direct. Usage: sort <n> <rounds>
// <seed>.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn check(xs: []const u64) u64 {
    var sum: u64 = 0;
    for (xs, 0..) |x, i| {
        if (i > 0 and xs[i - 1] > x) return 0xdead;
        sum +%= x *% @as(u64, i % 1000 + 1);
    }
    return sum;
}

fn fill(xs: []u64, p: usize, st0: u64) u64 {
    var st = st0;
    const n = xs.len;
    for (xs, 0..) |*x, i| {
        if (p == 0) {
            st +%= 0x9e3779b97f4a7c15;
            x.* = mix(st);
        } else if (p == 1) {
            x.* = @as(u64, n - i);
        } else if (p == 2) {
            x.* = @as(u64, i % 1000);
        } else {
            st +%= 0x9e3779b97f4a7c15;
            x.* = mix(st) >> 60;
        }
    }
    return st;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    const xs = try pa.alloc(u64, n);
    defer pa.free(xs);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        for (0..4) |p| {
            st = fill(xs, p, st);
            std.sort.pdq(u64, xs, {}, std.sort.asc(u64));
            total +%= check(xs);
        }
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
