// sortstable, Zig, idiomatic std: the task of README.md with std.mem.sort
// (block sort, in place, stable).
const std = @import("std");

const Rec = struct { key: u32, id: u32 };

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn lessKey(_: void, p: Rec, q: Rec) bool {
    return p.key < q.key;
}

fn fold(xs: []const Rec) u64 {
    const n = xs.len;
    var sum = @as(u64, xs[n - 1].key) *% 1000003 +% @as(u64, xs[n - 1].id);
    var i: usize = 0;
    while (i < n) : (i += 1024) sum +%= @as(u64, xs[i].id) *% @as(u64, i);
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    const xs = try std.heap.page_allocator.alloc(Rec, n);
    defer std.heap.page_allocator.free(xs);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        var st = seed +% r *% 0x5555555555555555;
        for (xs, 0..) |*x, i| {
            st +%= 0x9e3779b97f4a7c15;
            x.* = .{ .key = @intCast(mix(st) % 1000), .id = @intCast(i) };
        }
        std.mem.sort(Rec, xs, {}, lessKey);
        total +%= fold(xs);
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
