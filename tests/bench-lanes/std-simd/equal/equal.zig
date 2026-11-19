// equal, Zig, SIMD: the kernel of README.md with @Vector(W, u8), W
// std.simd.suggestVectorLength(u8) at the build's fixed CPU target (16 at
// x86-64-v2, 32 at x86-64-v3) and no run-time dispatch (B16). Four vector
// XORs ORed and tested once a step (@reduce(.Or)), then one vector a step,
// then bytes: equal.c's method and std.simd.Equal's. Both buffers start on
// a 64-byte boundary, as in every column (BS-4). Usage: equal <size> <reps>
// <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(u8) orelse 16;
const V = @Vector(W, u8);

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn load(s: []const u8, i: usize) V {
    return s[i..][0..W].*;
}

fn equal(a: []const u8, b: []const u8) bool {
    if (a.len != b.len) return false;
    const n = a.len;
    var i: usize = 0;
    while (i + 4 * W <= n) : (i += 4 * W) {
        const x0 = load(a, i) ^ load(b, i);
        const x1 = load(a, i + W) ^ load(b, i + W);
        const x2 = load(a, i + 2 * W) ^ load(b, i + 2 * W);
        const x3 = load(a, i + 3 * W) ^ load(b, i + 3 * W);
        if (@reduce(.Or, (x0 | x1) | (x2 | x3)) != 0) return false;
    }
    while (i + W <= n) : (i += W) {
        if (@reduce(.Or, load(a, i) ^ load(b, i)) != 0) return false;
    }
    while (i < n) : (i += 1) {
        if (a[i] != b[i]) return false;
    }
    return true;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    // BS-4: both buffers start on a 64-byte boundary in every column.
    const a = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(a);
    const b = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(b);
    if (@intFromPtr(a.ptr) % 64 != 0 or @intFromPtr(b.ptr) % 64 != 0) {
        std.debug.print("equal: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    for (a, b, 0..) |*x, *y, i| {
        x.* = @truncate((@as(u64, i) *% 0x9e3779b97f4a7c15 +% st) >> 56);
        y.* = x.*;
    }
    const span: u64 = if (n < 64) n else 64;
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const m = mix(st);
        const pos = n - 1 - @as(usize, @intCast(m % span));
        const d: u8 = @truncate((m >> 32) & 1);
        b[pos] ^= d;
        total = total *% 31 +% @intFromBool(equal(a, b));
        b[pos] ^= d;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
