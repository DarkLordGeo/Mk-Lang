// count, Zig, SIMD: the kernel of README.md with @Vector(W, u8), W
// std.simd.suggestVectorLength(u8) at the build's fixed CPU target (16 at
// x86-64-v2, 32 at x86-64-v3) and no run-time dispatch (B16). Each compare
// is selected to 1 or 0 and added into one of four u8 counters, a step of
// four vectors, and every 255 steps the counters are widened and summed
// with @reduce(.Add); then one vector a step (@popCount of the compare
// bitcast to a W-bit integer), then bytes: count.c's method and
// std.simd.Count's. The buffer starts on a 64-byte boundary, as in every
// column (BS-4). Usage: count <size> <reps> <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(u8) orelse 16;
const V = @Vector(W, u8);
const Wide = @Vector(W, u32);
const Bits = switch (W) {
    16 => u16,
    32 => u32,
    64 => u64,
    else => @compileError("unexpected vector width"),
};

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn load(s: []const u8, i: usize) V {
    return s[i..][0..W].*;
}

fn count(s: []const u8, b: u8) i64 {
    const n = s.len;
    const nd: V = @splat(b);
    const one: V = @splat(1);
    const zero: V = @splat(0);
    var total: i64 = 0;
    var i: usize = 0;
    while (i + 4 * W <= n) {
        var c: [4]V = .{ zero, zero, zero, zero };
        var steps: usize = 0;
        while (steps < 255 and i + 4 * W <= n) : ({
            steps += 1;
            i += 4 * W;
        }) {
            inline for (0..4) |k| c[k] += @select(u8, load(s, i + k * W) == nd, one, zero);
        }
        const sum: Wide = @as(Wide, c[0]) + @as(Wide, c[1]) + @as(Wide, c[2]) + @as(Wide, c[3]);
        total += @reduce(.Add, sum);
    }
    while (i + W <= n) : (i += W) {
        const m: Bits = @bitCast(load(s, i) == nd);
        total += @popCount(m);
    }
    while (i < n) : (i += 1) {
        if (s[i] == b) total += 1;
    }
    return total;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    const buf = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(buf);
    if (@intFromPtr(buf.ptr) % 64 != 0) {
        std.debug.print("count: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    for (buf, 0..) |*c, i| c.* = @truncate((@as(u64, i) *% 0x9e3779b97f4a7c15 +% st) >> 60);
    var total: u64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const c = count(buf, @truncate(mix(st) & 15));
        total = total *% 31 +% @as(u64, @intCast(c));
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
