// fmt, Zig, idiomatic std: the task of README.md with std.fmt.bufPrint
// ("{d}": decimal; for f64 the shortest digits that round-trip) and
// std.fmt.parseInt and std.fmt.parseFloat. Usage: fmt <n> <rounds> <seed>.
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
    const n = try std.fmt.parseInt(u64, args[1], 10);
    const rounds = try std.fmt.parseInt(u64, args[2], 10);
    var st = try std.fmt.parseInt(u64, args[3], 10);
    var buf: [64]u8 = undefined;
    var sum: i64 = 0;
    var ok: i64 = 0;
    var r: u64 = 0;
    while (r < rounds) : (r += 1) {
        var i: u64 = 0;
        while (i < n) : (i += 1) {
            st +%= 0x9e3779b97f4a7c15;
            const z = mix(st);
            var x: i64 = @intCast(z >> 1);
            if (z & 1 == 1) x = -x;
            st +%= 0x9e3779b97f4a7c15;
            const f = @as(f64, @floatFromInt(mix(st) >> 11)) * (1.0 / 9007199254740992.0) * 1e6;
            const s1 = try std.fmt.bufPrint(&buf, "{d}", .{x});
            const y = try std.fmt.parseInt(i64, s1, 10);
            const s2 = try std.fmt.bufPrint(&buf, "{d}", .{f});
            const g = try std.fmt.parseFloat(f64, s2);
            sum = sum +% y +% @as(i64, @intFromFloat(g));
            if (g == f) ok += 1;
        }
    }
    var out: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &out);
    try w.interface.print("{d}\n", .{sum +% ok});
    try w.interface.flush();
}
