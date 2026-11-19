// fmtints, Zig: the reference algorithm of README.md. The textbook
// conversion written out (one digit per division by 10 into a [20]u8
// temporary, copied out byte by byte), not std.fmt: the idiomatic fmt row
// compares the std formatters. A []u8 output buffer from
// std.heap.page_allocator indexed buf[pos], usize positions (the type Zig
// indexes with) and i64 sums like C's int64_t (+ checked in ReleaseSafe, UB
// in ReleaseFast). splitmix64 uses +% and *%.
// No libc.
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
    const pa = std.heap.page_allocator;
    const xs = try pa.alloc(i64, n);
    for (0..n) |i| {
        st +%= 0x9e3779b97f4a7c15;
        xs[i] = @as(i64, @intCast(mix(st) % 2000000001)) - 1000000000;
    }
    const buf = try pa.alloc(u8, n * 12);
    var tmp: [20]u8 = @splat(0);
    var total: i64 = 0;
    for (0..rounds) |_| {
        var pos: usize = 0;
        for (0..n) |i| {
            const x = xs[i];
            var u: u64 = undefined;
            if (x < 0) {
                buf[pos] = 45;
                pos += 1;
                u = @intCast(-x);
            } else {
                u = @intCast(x);
            }
            var k: usize = 20;
            while (true) {
                k -= 1;
                tmp[k] = 48 + @as(u8, @intCast(u % 10));
                u = u / 10;
                if (u == 0) {
                    break;
                }
            }
            for (k..20) |j| {
                buf[pos] = tmp[j];
                pos += 1;
            }
            buf[pos] = 10;
            pos += 1;
        }
        var sum: i64 = 0;
        for (0..pos) |i| {
            sum += buf[i];
        }
        total += sum;
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{total});
    try w.interface.flush();
}
