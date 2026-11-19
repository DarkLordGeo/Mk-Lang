// indexany, Zig, SIMD: the kernel of README.md with @Vector(W, u8), W
// std.simd.suggestVectorLength(u8) at the build's fixed CPU target (16 at
// x86-64-v2, 32 at x86-64-v3) and no run-time dispatch (B16). @Vector has no
// run-time byte shuffle (@shuffle's mask is comptime), so Truffle's table
// lookups cannot be written in it; the set test is one compare per set byte,
// ORed (inline for over the row's fixed set), one vector a step, the first
// member by @ctz of the result bitcast to a W-bit integer, then bytes.
// Labelled so in the README. The buffer starts on a 64-byte boundary, as in
// every column (BS-4). Usage: indexany <size> <reps> <seed>.
const std = @import("std");

const W = std.simd.suggestVectorLength(u8) orelse 16;
const V = @Vector(W, u8);
const B = @Vector(W, bool);
const Bits = switch (W) {
    16 => u16,
    32 => u32,
    64 => u64,
    else => @compileError("unexpected vector width"),
};

const SET = [8]u8{ '\t', '\n', '\r', ' ', '"', ',', ':', '=' };

fn mix(x: u64) u64 {
    var z = x;
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9;
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb;
    return z ^ (z >> 31);
}

fn load(s: []const u8, i: usize) V {
    return s[i..][0..W].*;
}

fn member(x: u8, set: []const u8) bool {
    for (set) |c| {
        if (x == c) return true;
    }
    return false;
}

fn indexAny(s: []const u8, set: *const [8]u8) i64 {
    const n = s.len;
    var i: usize = 0;
    while (i + W <= n) : (i += W) {
        const v = load(s, i);
        var hit: B = @splat(false);
        inline for (0..8) |k| hit = hit | (v == @as(V, @splat(set[k])));
        const m: Bits = @bitCast(hit);
        if (m != 0) return @intCast(i + @ctz(m));
    }
    while (i < n) : (i += 1) {
        if (member(s[i], set)) return @intCast(i);
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
    // BS-4: the buffer starts on a 64-byte boundary in every column.
    const buf = try pa.alignedAlloc(u8, .@"64", n);
    defer pa.free(buf);
    if (@intFromPtr(buf.ptr) % 64 != 0) {
        std.debug.print("indexany: buffer not 64-byte aligned\n", .{});
        std.process.exit(3);
    }
    for (buf, 0..) |*c, i| c.* = 0x40 | @as(u8, @truncate((@as(u64, i) *% 131 +% st) & 0x3F));
    const span: u64 = if (n < 64) n else 64;
    var total: i64 = 0;
    var r: u64 = 0;
    while (r < reps) : (r += 1) {
        st +%= 0x9e3779b97f4a7c15;
        const m = mix(st);
        const pos = n - 1 - @as(usize, @intCast(m % span));
        const old = buf[pos];
        buf[pos] = SET[@intCast((m >> 32) & 7)];
        total += indexAny(buf, &SET);
        buf[pos] = old;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
