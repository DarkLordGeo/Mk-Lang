// digits, Zig, idiomatic std: std.fmt.bufPrint "{e}" (variant shortest;
// std.fmt.float's Ryu, shortest round-trip) over 4,096 positive f64
// inputs built from splitmix64 bits (README.md), reps passes, pass r
// starting at input r. The digits and exponent are read back from the
// text, trailing zeros dropped, and folded as every column folds them.
// Variant exact17 is skipped (bench.json): Zig's precision form rounds the
// shortest digits, not the exact value.
// Usage: digits <variant> <reps> <seed>.
const std = @import("std");

const N: usize = 4096;

fn mix(z0: u64) u64 {
    var z = z0;
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9;
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB;
    return z ^ (z >> 31);
}

fn fold(b: []const u8) !u64 {
    var k: usize = 0;
    while (k < b.len and b[k] != 'e') k += 1;
    var last: usize = 0;
    for (0..k) |i| {
        if (b[i] != '.' and b[i] != '0') last = i + 1;
    }
    var len: u64 = 0;
    var sum: u64 = 0;
    for (b[0..last], 0..) |c, i| {
        if (c != '.') {
            len += 1;
            if (i + 1 < last) sum += c - '0';
        }
    }
    const e = try std.fmt.parseInt(i64, b[k + 1 ..], 10);
    return len *% 1000003 +% @as(u64, @intCast(e + 400)) *% 1009 +% sum;
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 4) return error.Usage;
    if (!std.mem.eql(u8, args[1], "shortest")) return error.Usage;
    const reps = try std.fmt.parseInt(usize, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    var xs: [N]f64 = undefined;
    for (&xs) |*x| {
        st +%= 0x9E3779B97F4A7C15;
        const z = mix(st);
        const e: u64 = 723 + (z >> 53) % 601;
        x.* = @bitCast(e << 52 | (z & 0x000FFFFFFFFFFFFF));
    }
    var acc: u64 = 0;
    var buf: [64]u8 = undefined;
    for (0..reps) |r| {
        for (0..N) |i| {
            const s = try std.fmt.bufPrint(&buf, "{e}", .{xs[(i + r) & (N - 1)]});
            acc +%= try fold(s);
        }
    }
    var obuf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &obuf);
    try w.interface.print("{d}\n", .{acc});
    try w.interface.flush();
}
