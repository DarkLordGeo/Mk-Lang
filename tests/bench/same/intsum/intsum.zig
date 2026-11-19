// intsum, Zig: the reference algorithm of README.md.
// A slice from std.heap.page_allocator indexed with xs[i]. The sum's + is
// plain i64 +: checked (panics) in ReleaseSafe, unchecked (UB) in
// ReleaseFast. The fill uses +% and *% because splitmix64 wraps by design.
// No @setRuntimeSafety, no libc.
const std = @import("std");

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const rounds = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const xs = try std.heap.page_allocator.alloc(i64, n);
    defer std.heap.page_allocator.free(xs);
    for (0..n) |i| {
        st +%= 0x9e3779b97f4a7c15;
        xs[i] = @intCast(mix(st) & 1023);
    }
    var total: i64 = 0;
    for (0..rounds) |r| {
        var s: i64 = 0;
        for (0..n) |i| {
            s += xs[i];
        }
        total += s;
        xs[r % n] = s & 1023;
    }
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
