// sum, Zig, SIMD: the kernel of README.md with @Vector(W, f32), W
// std.simd.suggestVectorLength(f32) at the build's fixed CPU target (4 at
// x86-64-v2, 8 at x86-64-v3) and no run-time dispatch (B16): 16 / W vectors
// hold the 16 partial sums, then the pairwise tree and the tail in order,
// so every column does the same f32 arithmetic in the same order. The
// buffer starts on a 64-byte boundary, as in every column (BS-4). Usage:
// sum <n> <reps> <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(f32) orelse 4;
const V = @Vector(W, f32);
const K = 16 / W;

fn load(s: []const f32, i: usize) V {
    return s[i..][0..W].*;
}

fn sum(x: []const f32) f32 {
    const n = x.len;
    var acc: [K]V = @splat(@as(V, @splat(0)));
    var i: usize = 0;
    while (i + 16 <= n) : (i += 16) {
        inline for (0..K) |k| acc[k] = acc[k] + load(x, i + k * W);
    }
    var s: [16]f32 = undefined;
    inline for (0..K) |k| {
        const a: [W]f32 = acc[k];
        @memcpy(s[k * W ..][0..W], &a);
    }
    var w: usize = 8;
    while (w >= 1) : (w /= 2) {
        for (0..w) |j| s[j] = s[j] + s[j + w];
    }
    var r = s[0];
    while (i < n) : (i += 1) {
        r = r + x[i];
    }
    return r;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    const seed = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    const x = try pa.alignedAlloc(f32, .@"64", n);
    defer pa.free(x);
    if (@intFromPtr(x.ptr) % 64 != 0) {
        std.debug.print("sum: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    for (x, 0..) |*a, i| {
        const u: u64 = i;
        a.* = @as(f32, @floatFromInt((u *% 0x9e3779b97f4a7c15 +% seed) >> 48)) * (1.0 / 65536.0) - 0.5;
    }
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        x[@intCast(r % n)] += 1.0;
        const bits: u32 = @bitCast(sum(x));
        total +%= bits;
    }
    var out: [64]u8 = undefined;
    var wr = std.Io.File.stdout().writer(init.io, &out);
    try wr.interface.print("{d}\n", .{total});
    try wr.interface.flush();
}
