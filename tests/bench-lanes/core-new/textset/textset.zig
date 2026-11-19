// textset, Zig, idiomatic std: the task of README.md with one
// std.ArrayList(u8) per round over std.heap.smp_allocator, overwritten by
// clearRetainingCapacity then appendSlice.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn round(gpa: std.mem.Allocator, src: []const u8, n: usize, seed: u64) !u64 {
    var t: std.ArrayList(u8) = .empty;
    defer t.deinit(gpa);
    var sum: u64 = 0;
    var st = seed;
    var i: usize = 0;
    while (i < n) : (i += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const v = mix(st);
        const k: usize = @intCast(v % 300);
        const off: usize = @intCast((v >> 16) % 3796);
        t.clearRetainingCapacity();
        try t.appendSlice(gpa, src[off .. off + k]);
        const s = t.items;
        sum +%= s.len;
        if (s.len > 0) {
            sum +%= @as(u64, s[0]) *% 7 +% @as(u64, s[s.len - 1]);
        }
    }
    return sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    var buf: [4096]u8 = undefined;
    var st = seed ^ 0x5555555555555555;
    for (&buf) |*b| {
        st +%= 0x9e3779b97f4a7c15;
        b.* = @as(u8, @intCast(mix(st) % 26)) + 'a';
    }
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) total +%= try round(std.heap.smp_allocator, &buf, n, seed +% r);
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
