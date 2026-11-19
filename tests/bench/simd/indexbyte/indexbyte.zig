// indexbyte, Zig, SIMD: the kernel of README.md with @Vector(W, u8), W
// std.simd.suggestVectorLength(u8) at the build's fixed CPU target (16 at
// x86-64-v2, 32 at x86-64-v3) and no run-time dispatch. Four vectors a
// step, their compares ORed and reduced with @reduce(.Or); then one vector a
// step, the first set lane found by @ctz of the compare bitcast to a W-bit
// integer; then bytes. The buffer starts on a 64-byte boundary, as in every
// column. Usage: indexbyte <size> <reps> <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(u8) orelse 16;
const V = @Vector(W, u8);
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

fn indexByte(s: []const u8, b: u8) i64 {
    const n = s.len;
    const nb: V = @splat(b);
    var i: usize = 0;
    while (i + 4 * W <= n) : (i += 4 * W) {
        const e0 = load(s, i) == nb;
        const e1 = load(s, i + W) == nb;
        const e2 = load(s, i + 2 * W) == nb;
        const e3 = load(s, i + 3 * W) == nb;
        const any = (e0 | e1) | (e2 | e3);
        if (@reduce(.Or, any)) break;
    }
    while (i + W <= n) : (i += W) {
        const m: Bits = @bitCast(load(s, i) == nb);
        if (m != 0) return @intCast(i + @ctz(m));
    }
    while (i < n) : (i += 1) {
        if (s[i] == b) return @intCast(i);
    }
    return -1;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const reps = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    const pa = std.heap.page_allocator;
    // The buffer starts on a 64-byte boundary in every column.
    const buf = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(buf);
    if (@intFromPtr(buf.ptr) % 64 != 0) {
        std.debug.print("indexbyte: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    for (buf, 0..) |*c, i| c.* = @truncate((@as(u64, i) *% 131 +% st) & 0x7F);
    const span: u64 = if (n < 64) n else 64;
    var total: i64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const pos = n - 1 - @as(usize, @intCast(mix(st) % span));
        const old = buf[pos];
        buf[pos] = 0xFF;
        total += indexByte(buf, 0xFF);
        buf[pos] = old;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
