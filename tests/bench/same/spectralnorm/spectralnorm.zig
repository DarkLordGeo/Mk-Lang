// spectralnorm, Zig: the reference algorithm of README.md (the Benchmarks
// Game's spectral-norm, scalar and single-threaded). A(i, j) computed from
// i64 on every use (checked in ReleaseSafe, UB in ReleaseFast), []f64 vectors
// from std.heap.page_allocator indexed v[j], @sqrt. No @Vector, no
// @setFloatMode, no libc.
const std = @import("std");

fn a(i: i64, j: i64) f64 {
    return 1.0 / @as(f64, @floatFromInt(@divTrunc((i + j) * (i + j + 1), 2) + i + 1));
}

fn mulAv(v: []const f64, out: []f64) void {
    const n = v.len;
    for (0..n) |i| {
        var s: f64 = 0.0;
        for (0..n) |j| {
            s = s + a(@intCast(i), @intCast(j)) * v[j];
        }
        out[i] = s;
    }
}

fn mulAtv(v: []const f64, out: []f64) void {
    const n = v.len;
    for (0..n) |i| {
        var s: f64 = 0.0;
        for (0..n) |j| {
            s = s + a(@intCast(j), @intCast(i)) * v[j];
        }
        out[i] = s;
    }
}

fn mulAtAv(v: []const f64, out: []f64, tmp: []f64) void {
    mulAv(v, tmp);
    mulAtv(tmp, out);
}

pub fn main(init: std.process.Init) !void {
    const args = try init.minimal.args.toSlice(init.arena.allocator());
    if (args.len != 2) return error.Usage;
    const n = try std.fmt.parseInt(usize, args[1], 10);
    const pa = std.heap.page_allocator;
    const u = try pa.alloc(f64, n);
    const v = try pa.alloc(f64, n);
    const tmp = try pa.alloc(f64, n);
    @memset(u, 1.0);
    @memset(v, 0.0);
    for (0..10) |_| {
        mulAtAv(u, v, tmp);
        mulAtAv(v, u, tmp);
    }
    var vbv: f64 = 0.0;
    var vv: f64 = 0.0;
    for (0..n) |i| {
        vbv = vbv + u[i] * v[i];
        vv = vv + v[i] * v[i];
    }
    const norm: i64 = @intFromFloat(@sqrt(vbv / vv) * 1e9);
    var buf: [64]u8 = undefined;
    var w = std.Io.File.stdout().writer(init.io, &buf);
    try w.interface.print("{d}\n", .{norm});
    try w.interface.flush();
}
